# Runtime image for the proof-of-space-time bond-cost field test. It carries the
# prebuilt `silt` binary and nothing else the daemon does not need — one image
# plays every role in this harness (honest validator, its auditing peer, the
# under-bonded adversary), differing only by the daemon flags in the compose
# file.
#
# The binary is built on the host and COPYed in (not compiled in-container): it
# keeps this image tiny, avoids a ~1 GB Go-build memory spike (so it runs on
# small hosts too), and lets CI reuse the compile it already did. run.sh does the
# host build; see it for the exact `go build` invocation. `coreutils` is present
# so run.sh can `du`/`stat`/`date` the on-disk plot for the disk + timing numbers
# this test reports.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends coreutils procps ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
RUN chmod +x /usr/local/bin/silt
ENTRYPOINT []
CMD ["silt", "help"]
