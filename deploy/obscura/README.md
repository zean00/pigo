# Obscura Rendered Scraping

This Compose template starts a local Obscura CDP server for pigo rendered scraping.

```bash
cd deploy/obscura
docker compose up -d --build
export PIGO_OBSCURA_URL=http://localhost:9222
```

The service runs:

```bash
obscura serve --port 9222 --stealth --obey-robots --workers ${OBSCURA_WORKERS:-1}
```

Useful overrides:

```bash
OBSCURA_VERSION=v0.1.2 OBSCURA_PORT=9222 OBSCURA_WORKERS=2 docker compose up -d --build
```

Then call the `scrape` research tool with `engine: "obscura"` or `render: true`.
