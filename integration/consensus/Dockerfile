# Runtime image for the multi-validator consensus / partition-heal harness.
# It carries the prebuilt `silt` binary; ONE image plays every role in the
# topology (each container is just a `silt daemon` with different flags).
#
# The binary is built on the host and COPYed in (not compiled in-container):
# it keeps this image tiny, avoids a ~1 GB Go-build memory spike (so it runs
# on small hosts), and lets CI reuse the compile it already did. run.sh does
# the host build; see it for the exact `go build` invocation.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates procps iputils-ping \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
RUN chmod +x /usr/local/bin/silt
ENTRYPOINT []
CMD ["silt", "help"]
