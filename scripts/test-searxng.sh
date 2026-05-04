#!/usr/bin/env bash
set -euo pipefail

name="${PIGO_SEARXNG_CONTAINER:-pigo-searxng-smoke}"
port="${PIGO_SEARXNG_PORT:-18080}"
image="${PIGO_SEARXNG_IMAGE:-searxng/searxng:latest}"
go_image="${PIGO_SEARXNG_GO_IMAGE:-golang:1.26}"
network="${name}-net"
test_url="http://searxng:8080"
local_url="http://127.0.0.1:${port}"
settings_dir="$(mktemp -d)"

cleanup() {
  docker rm -f "${name}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
  rm -rf "${settings_dir}"
}
trap cleanup EXIT

docker rm -f "${name}" >/dev/null 2>&1 || true
docker network rm "${network}" >/dev/null 2>&1 || true
docker network create "${network}" >/dev/null

cat >"${settings_dir}/settings.yml" <<EOF
use_default_settings: true
server:
  bind_address: "0.0.0.0"
  port: 8080
  secret_key: "pigo-local-searxng-smoke"
search:
  formats:
    - html
    - json
EOF

docker run -d --name "${name}" --network "${network}" --network-alias searxng -p "127.0.0.1:${port}:8080" \
  -v "${settings_dir}/settings.yml:/etc/searxng/settings.yml:ro" \
  -e SEARXNG_BASE_URL="${local_url}/" \
  "${image}" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${name}" wget -q -O /dev/null "http://127.0.0.1:8080/search?q=pigo&format=json" >/dev/null 2>&1; then
    docker run --rm --network "${network}" \
      -v "${PWD}:/work:ro" \
      -w /work \
      -e GOMODCACHE=/tmp/gomodcache \
      -e GOCACHE=/tmp/gocache \
      -e PIGO_LIVE_RESEARCH_TESTS=1 \
      -e PIGO_SEARXNG_URL="${test_url}" \
      "${go_image}" go test ./pkg/researchadapter -run TestLiveResearchSearch -count=1
    exit 0
  fi
  sleep 1
done

docker logs "${name}" >&2 || true
echo "SearXNG did not become ready at ${local_url}" >&2
exit 1
