# The core image, ghcr.io/itskontra/ddash:<release>. It holds only the core
# binary (shell, @ddash/ui and vendor ESM embedded); the core downloads its
# Release's Plugins and Components from the GitHub release into /data.
#
# Built by `make image` from the context bin/image/, which holds the static
# core binary (`ddash`) and an empty `data/`:
#
#	make image RELEASE=v1.2.3
#
# distroless/static: no shell, no libc, CA certificates and tzdata included.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

# The data dir, owned by the user ddash runs as, so the image also runs
# without a bind mount. compose bind-mounts /opt/ddash/data over it.
COPY --chown=65532:65532 data/ /data/
COPY ddash /ddash

# Links the GHCR package to the repo.
LABEL org.opencontainers.image.source=https://github.com/itsKontra/ddash
USER 65532:65532
# ddash listens on :8080 and keeps its data in /data by default.
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD ["/ddash", "healthcheck"]
ENTRYPOINT ["/ddash"]
