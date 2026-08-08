# A single machine-readable map the orchestrator consumes: role/zone/internal IP
# + external IP (empty for natted nodes) per node. The orchestrator reaches every
# node — natted included — over `gcloud compute ssh --tunnel-through-iap`, so it
# does not depend on external IPs.

locals {
  all_instances = merge(
    { for k, v in google_compute_instance.public : k => v },
    { for k, v in google_compute_instance.natted : k => v },
    { for k, v in google_compute_instance.natgw : k => v },
  )
}

output "nodes" {
  value = {
    for k, inst in local.all_instances : k => {
      instance_name = inst.name
      role          = var.nodes[k].role
      zone          = inst.zone
      internal_ip   = var.nodes[k].ip
      external_ip   = length(inst.network_interface[0].access_config) > 0 ? inst.network_interface[0].access_config[0].nat_ip : ""
    }
  }
}

output "artifacts_bucket" {
  value = google_storage_bucket.artifacts.name
}

output "run_id" {
  value = var.run_id
}
