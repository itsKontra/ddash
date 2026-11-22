# The Docker Agent's image, ghcr.io/itskontra/ddash-docker-agent:<release>:
# the static ddash-docker-agent on the official Docker CLI image, which
# carries the Compose plugin the Agent runs. Built by
# `make docker-agent-image` from bin/docker-agent/.
#
# It holds root-equivalent power over its host through the mounted Docker
# socket; the Operator installs it only on hosts ddash should control.
FROM docker:29.10-cli

COPY ddash-docker-agent /usr/local/bin/ddash-docker-agent

LABEL org.opencontainers.image.source=https://github.com/itsKontra/ddash
# HTTPS, self-signed; ddash pins its certificate at pairing.
EXPOSE 7443
# Its certificate and pairing.
VOLUME /data
ENTRYPOINT ["/usr/local/bin/ddash-docker-agent"]
CMD ["serve"]
