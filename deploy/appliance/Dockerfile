# syntax=docker/dockerfile:1.7
# Omni-Data-Bus all-in-one image (OC-072): the app plus a single-node Couchbase Server Community
# Edition that holds its metadata and can be a migration target. Build it with `make appliance`,
# which first builds the regular image (deploy/docker/Dockerfile) for the same platform and copies
# its `odb` binary from there, so both images run byte-identical binaries.
#
#   make appliance [ARCH=amd64|arm64] [VERSION=…]
#   docker run -d -p 8080:8080 -v odb-data:/opt/couchbase/var --memory 4g --stop-timeout 90 omni-data-bus-appliance:<version>

# The regular image for the same platform (make appliance passes it).
ARG ODB_IMAGE=omni-data-bus:dev
# Couchbase Server CE, pinned by digest: the newest 8.0.x community tag, published for amd64 and arm64.
ARG CB_VERSION=8.0.2
ARG CB_IMAGE=couchbase/server:community-8.0.2@sha256:aab0722a4f7098f7b325d02968156a531c717ced5e7e4e8ea2ba463f2fa6c0dd

FROM ${ODB_IMAGE} AS odb

FROM ${CB_IMAGE}
ARG CB_VERSION
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=""
ARG SOURCE_URL=""
LABEL org.opencontainers.image.title="Omni-Data-Bus (all-in-one)" \
      org.opencontainers.image.description="Omni-Data-Bus with a bundled single-node Couchbase Server Community Edition" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="${SOURCE_URL}" \
      org.opencontainers.image.licenses="Apache-2.0 AND LicenseRef-Couchbase-CE" \
      org.opencontainers.image.vendor="Couchbase, Inc." \
      io.omnidatabus.couchbase.edition="community" \
      io.omnidatabus.couchbase.version="${CB_VERSION}"
# tini is PID 1: it reaps zombies and forwards signals to the supervisor.
RUN apt-get update \
 && apt-get install -y --no-install-recommends tini \
 && rm -rf /var/lib/apt/lists/*
COPY --from=odb /usr/local/bin/odb /usr/local/bin/odb
COPY deploy/compose/couchbase/init.sh /usr/local/lib/odb/cb-init.sh
COPY deploy/appliance/odb-appliance.sh /usr/local/bin/odb-appliance
# Omni-Data-Bus's LICENSE, NOTICE and third-party notices (OC-189), and this image's own notice:
# the image as a whole is Apache-2.0 for our part and Couchbase CE under its own licence.
COPY --from=odb /usr/share/doc/omni-data-bus/ /usr/share/doc/omni-data-bus/
COPY deploy/appliance/NOTICE /usr/share/doc/omni-data-bus/NOTICE.all-in-one
ENV ODB_CB_VERSION=${CB_VERSION} \
    OC_HTTP_ADDR=:8080
EXPOSE 8080 8091
# Everything lives here: Couchbase's data, plus odb/ (generated secrets, directory-target output).
VOLUME /opt/couchbase/var
HEALTHCHECK --interval=10s --timeout=5s --start-period=240s --retries=3 CMD ["/usr/local/bin/odb", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/odb-appliance"]
