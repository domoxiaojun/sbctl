# Built binaries are cross-compiled with CGO_ENABLED=0 before this image is built.
FROM debian:trixie-slim
ARG TARGETARCH
ARG BUILD_REVISION=local
ARG BUILD_VERSION=0.12.0-rc.2
LABEL org.opencontainers.image.source="https://github.com/domoxiaojun/sbctl" \
      org.opencontainers.image.revision="${BUILD_REVISION}" \
      org.opencontainers.image.version="${BUILD_VERSION}"
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 sbctl && useradd -u 10001 -g 10001 -M sbctl \
    && groupadd -g 10003 sbctl-public && useradd -u 10003 -g 10003 -M sbctl-public
COPY dist/linux-${TARGETARCH}/sbctl /opt/sbctl/bin/sbctl
COPY web/dist /opt/sbctl/web
USER 10001:10001
ENTRYPOINT ["/opt/sbctl/bin/sbctl"]
CMD ["serve", "--listen", ":8080"]
