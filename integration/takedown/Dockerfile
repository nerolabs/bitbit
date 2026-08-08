# Runtime image for the takedown integration harness. It carries the prebuilt
# `silt` binary plus iproute2 (so the entrypoint can self-advertise the
# container's IP) and coreutils (base64/od, used by run.sh to convert a link's
# base64url root into the hex form the -denylist file and -revoke flag want).
# ONE image plays every role: chain-backed validator/seed and plain holders.
#
# The binary is built on the host and COPYed in (not compiled in-container):
# it keeps this image tiny, avoids a ~1 GB Go-build memory spike, and lets CI
# reuse the compile it already did. run.sh does the host build; see it for the
# exact `go build` invocation.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends iproute2 coreutils ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
COPY node.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/silt /usr/local/bin/node.sh
ENTRYPOINT []
CMD ["silt", "help"]
