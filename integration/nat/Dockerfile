# Runtime image for the NAT integration harness. It carries the prebuilt
# `silt` binary plus iproute2 + iptables, so ONE image plays all three roles
# in the topology: relay/registry, NAT gateway (iptables MASQUERADE), and a
# NATed daemon (which re-points its own default route through the gateway).
#
# The binary is built on the host and COPYed in (not compiled in-container):
# it keeps this image tiny, avoids a ~1 GB Go-build memory spike (so it runs
# on small hosts), and lets CI reuse the compile it already did. run.sh does
# the host build; see it for the exact `go build` invocation.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends iproute2 iptables procps iputils-ping ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
COPY natgw.sh node.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/silt /usr/local/bin/natgw.sh /usr/local/bin/node.sh
ENTRYPOINT []
CMD ["silt", "help"]
