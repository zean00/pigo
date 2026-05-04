FROM ubuntu:22.04

ARG OBSCURA_VERSION=latest
ARG TARGETARCH

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tar \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) artifact="obscura-x86_64-linux.tar.gz" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    if [ "${OBSCURA_VERSION}" = "latest" ]; then \
      url="https://github.com/h4ckf0r0day/obscura/releases/latest/download/${artifact}"; \
    else \
      url="https://github.com/h4ckf0r0day/obscura/releases/download/${OBSCURA_VERSION}/${artifact}"; \
    fi; \
    curl -fsSL "$url" -o /tmp/obscura.tar.gz; \
    tar -xzf /tmp/obscura.tar.gz -C /usr/local/bin; \
    rm /tmp/obscura.tar.gz; \
    chmod +x /usr/local/bin/obscura /usr/local/bin/obscura-worker

EXPOSE 9222

ENTRYPOINT ["/usr/local/bin/obscura"]
CMD ["serve", "--port", "9222", "--stealth"]
