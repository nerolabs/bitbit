# Runtime image for the proof-of-retrieval (PoR) audit field test. It carries
# the prebuilt `silt` binary; one image plays every role in the topology:
# registry/bootstrap seed, honest holder, adversarial holder, and caretaker.
#
# The binary is built on the host and COPYed in (not compiled in-container):
# it keeps this image tiny, avoids a ~1 GB Go-build memory spike, and lets CI
# reuse the compile it already did. run.sh does the host build; see it for the
# exact `go build` invocation.
#
# entry.sh discovers the container's own IP and stamps it onto -advertise, so
# each daemon self-advertises correctly on the flat Docker network (a wildcard
# bind is never advertised on its own — see cmd/silt/daemon.go -advertise).
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends procps iproute2 ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
COPY entry.sh /usr/local/bin/entry.sh
RUN chmod +x /usr/local/bin/silt /usr/local/bin/entry.sh
ENTRYPOINT []
CMD ["silt", "help"]
