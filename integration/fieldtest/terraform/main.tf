# The ephemeral field-test network. Everything here is created on `apply` and
# removed on `destroy`. Instances are SPOT (preemptible) and self-destruct after
# var.ttl_minutes as a cost backstop — see the triple cost-guard in the README.

locals {
  # Split the node table by network role.
  public_nodes = { for k, v in var.nodes : k => v if v.role != "natted" && v.role != "natgw" }
  nat_nodes    = { for k, v in var.nodes : k => v if v.role == "natted" }
  natgw_nodes  = { for k, v in var.nodes : k => v if v.role == "natgw" }
  labels       = { fieldtest = var.run_id }
}

# ── Network ────────────────────────────────────────────────────────────────────
resource "google_compute_network" "silt" {
  name                    = "silt-ft-${var.run_id}"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "public" {
  name          = "silt-ft-public-${var.run_id}"
  ip_cidr_range = var.public_cidr
  region        = var.default_region
  network       = google_compute_network.silt.id
}

# The NAT subnet lives in the same region as the natgw. Its instances get NO
# external IP; their default route is the natgw instance, so they are genuinely
# un-dialable from the swarm and must reach it through the relay (or hole-punch).
resource "google_compute_subnetwork" "nat" {
  name          = "silt-ft-nat-${var.run_id}"
  ip_cidr_range = var.nat_cidr
  region        = var.default_region
  network       = google_compute_network.silt.id
}

# Route the NAT subnet's egress through the natgw instance (real NAT/conntrack).
resource "google_compute_route" "nat_egress" {
  count            = length(local.natgw_nodes) > 0 ? 1 : 0
  name             = "silt-ft-natroute-${var.run_id}"
  network          = google_compute_network.silt.id
  dest_range       = "0.0.0.0/0"
  next_hop_instance = google_compute_instance.natgw[one(keys(local.natgw_nodes))].self_link
  priority         = 900
  tags             = ["natted"]
}

# ── Firewall ───────────────────────────────────────────────────────────────────
# All traffic within the VPC is allowed (the swarm talks over internal IPs).
resource "google_compute_firewall" "internal" {
  name          = "silt-ft-internal-${var.run_id}"
  network       = google_compute_network.silt.id
  source_ranges = [var.public_cidr, var.nat_cidr]
  allow { protocol = "tcp" }
  allow { protocol = "udp" }
  allow { protocol = "icmp" }
}

# SSH from IAP only (35.235.240.0/20) — the orchestrator drives every node,
# including natted ones, over `gcloud compute ssh --tunnel-through-iap`.
resource "google_compute_firewall" "iap_ssh" {
  name          = "silt-ft-iap-ssh-${var.run_id}"
  network       = google_compute_network.silt.id
  source_ranges = ["35.235.240.0/20"]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}

# Public swarm/relay/registry reachability for the public-subnet nodes only.
resource "google_compute_firewall" "public_swarm" {
  name          = "silt-ft-swarm-${var.run_id}"
  network       = google_compute_network.silt.id
  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["public-node"]
  allow {
    protocol = "tcp"
    ports    = [tostring(var.swarm_port), tostring(var.relay_port), tostring(var.registry_port)]
  }
}

# ── The binary, shipped via GCS ────────────────────────────────────────────────
resource "google_storage_bucket" "artifacts" {
  name                        = "silt-ft-${var.run_id}-${var.project_id}"
  location                    = var.default_region
  force_destroy               = true
  uniform_bucket_level_access = true
  labels                      = local.labels
}

resource "google_storage_bucket_object" "silt_binary" {
  name   = "silt-linux-amd64"
  bucket = google_storage_bucket.artifacts.name
  source = var.silt_binary_path
}

# Let the VMs' default compute service account read the bucket.
data "google_compute_default_service_account" "default" {}

resource "google_storage_bucket_iam_member" "vm_read" {
  bucket = google_storage_bucket.artifacts.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${data.google_compute_default_service_account.default.email}"
}

# ── Startup script (shared by all silt nodes) ──────────────────────────────────
locals {
  ssh_keys_meta = var.ssh_pubkey == "" ? {} : { "ssh-keys" = var.ssh_pubkey }
}

# ── NAT gateway instance ───────────────────────────────────────────────────────
resource "google_compute_instance" "natgw" {
  for_each     = local.natgw_nodes
  name         = "silt-ft-${each.key}-${var.run_id}"
  machine_type = var.machine_type
  zone         = each.value.zone
  labels       = local.labels
  tags         = ["natgw", "public-node"]

  can_ip_forward = true # required to route the NAT subnet's traffic

  boot_disk {
    initialize_params {
      image = var.image
      size  = var.boot_disk_gb
    }
  }
  network_interface {
    subnetwork = google_compute_subnetwork.public.id
    network_ip = each.value.ip
    access_config {} # external IP so it can masquerade the NAT subnet out to the internet
  }
  scheduling {
    provisioning_model = "SPOT"
    preemptible        = true
    automatic_restart  = false
  }
  metadata = merge(local.ssh_keys_meta, {
    "startup-script" = templatefile("${path.module}/../provision/natgw-startup.sh", {
      nat_cidr    = var.nat_cidr
      ttl_minutes = var.ttl_minutes
    })
  })
}

# ── silt nodes on the public subnet ────────────────────────────────────────────
resource "google_compute_instance" "public" {
  for_each     = local.public_nodes
  name         = "silt-ft-${each.key}-${var.run_id}"
  machine_type = var.machine_type
  zone         = each.value.zone
  labels       = merge(local.labels, { role = each.value.role })
  tags         = ["public-node"]

  boot_disk {
    initialize_params {
      image = var.image
      size  = var.boot_disk_gb
    }
  }
  network_interface {
    subnetwork = google_compute_subnetwork.public.id
    network_ip = each.value.ip
    access_config {} # external IP; the swarm still advertises the internal IP
  }
  scheduling {
    provisioning_model = "SPOT"
    preemptible        = true
    automatic_restart  = false
  }
  service_account {
    email  = data.google_compute_default_service_account.default.email
    scopes = ["https://www.googleapis.com/auth/devstorage.read_only"]
  }
  metadata = merge(local.ssh_keys_meta, {
    "silt-argv"       = each.value.argv
    "silt-binary-url" = "gs://${google_storage_bucket.artifacts.name}/${google_storage_bucket_object.silt_binary.name}"
    "node-name"       = each.key
    "startup-script" = templatefile("${path.module}/../provision/silt-startup.sh", {
      ttl_minutes = var.ttl_minutes
    })
  })
  depends_on = [google_storage_bucket_iam_member.vm_read]
}

# ── silt nodes on the NAT subnet (no external IP) ───────────────────────────────
resource "google_compute_instance" "natted" {
  for_each     = local.nat_nodes
  name         = "silt-ft-${each.key}-${var.run_id}"
  machine_type = var.machine_type
  zone         = each.value.zone
  labels       = merge(local.labels, { role = each.value.role })
  tags         = ["natted"]

  boot_disk {
    initialize_params {
      image = var.image
      size  = var.boot_disk_gb
    }
  }
  network_interface {
    subnetwork = google_compute_subnetwork.nat.id
    network_ip = each.value.ip
    # NO access_config: no external IP → egress via the natgw, un-dialable inbound.
  }
  scheduling {
    provisioning_model = "SPOT"
    preemptible        = true
    automatic_restart  = false
  }
  service_account {
    email  = data.google_compute_default_service_account.default.email
    scopes = ["https://www.googleapis.com/auth/devstorage.read_only"]
  }
  metadata = merge(local.ssh_keys_meta, {
    "silt-argv"       = each.value.argv
    "silt-binary-url" = "gs://${google_storage_bucket.artifacts.name}/${google_storage_bucket_object.silt_binary.name}"
    "node-name"       = each.key
    "startup-script" = templatefile("${path.module}/../provision/silt-startup.sh", {
      ttl_minutes = var.ttl_minutes
    })
  })
  depends_on = [google_compute_instance.natgw, google_storage_bucket_iam_member.vm_read]
}

# ── Optional budget backstop ───────────────────────────────────────────────────
resource "google_billing_budget" "cap" {
  count           = var.budget_amount_usd > 0 && var.billing_account != "" ? 1 : 0
  billing_account = var.billing_account
  display_name    = "silt-fieldtest-${var.run_id}"
  budget_filter {
    projects = ["projects/${var.project_id}"]
  }
  amount {
    specified_amount {
      currency_code = "USD"
      units         = tostring(var.budget_amount_usd)
    }
  }
  threshold_rules { threshold_percent = 0.5 }
  threshold_rules { threshold_percent = 0.9 }
  threshold_rules { threshold_percent = 1.0 }
}
