# Runtime image for the economy + blind-credit field test. It carries the
# prebuilt `silt` binary plus iproute2 (node.sh reads the container's own IP to
# self-advertise on the flat bridge — a wildcard bind is never dialable on its
# own). ONE image plays every role: registry/issuer validators and holder nodes.
#
# The binary is built on the host and COPYed in (not compiled in-container): it
# keeps this image tiny, avoids a ~1 GB Go-build memory spike (so it runs on
# small hosts), and reuses the compile CI already did. run.sh does the host
# build; see it for the exact `go build` invocation.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends iproute2 procps ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
COPY node.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/silt /usr/local/bin/node.sh
ENTRYPOINT []
CMD ["silt", "help"]
