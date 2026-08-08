# Runtime image for the repair-under-churn integration harness. It carries the
# prebuilt `silt` binary and nothing else the test needs — no iproute2/iptables
# (unlike the NAT harness), because churn runs on one flat Docker network: the
# point here is real *node death* and real *repair*, not NAT traversal.
#
# The binary is built on the host and COPYed in (not compiled in-container),
# for the same reasons as integration/nat: it keeps the image tiny, avoids a
# ~1 GB Go-build memory spike inside Docker, and reuses the compile CI already
# did. run.sh does the host build; see it for the exact `go build` invocation.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY silt /usr/local/bin/silt
COPY role.sh /usr/local/bin/role.sh
RUN chmod +x /usr/local/bin/silt /usr/local/bin/role.sh
ENTRYPOINT []
CMD ["silt", "help"]
