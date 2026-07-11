# YANET2 Web UI

Web interface for YANET2 built with React and Gravity UI.

## Development

The web UI is an npm workspace, so dependencies install from the repository
root (a single hoisted `node_modules`):

```bash
npm install
```

Run the development server (from the repo root, or `cd web` and drop the flag):

```bash
npm run dev -w web
```

The development server runs on `http://localhost:3000` by default and proxies
`/api` requests to the gateway's HTTP endpoint.

### Dev server configuration

| Env var              | Default                  | Description                                      |
| -------------------- | ------------------------ | ------------------------------------------------ |
| `VITE_BACKEND_URL`   | `http://localhost:8081`  | Full URL of the gateway HTTP endpoint (proxy target). |
| `VITE_DEV_PORT`      | `3000`                   | Dev server listen port.                          |

```bash
# Point the dev proxy at a remote gateway
VITE_BACKEND_URL=http://10.0.0.5:8081 npm run dev -w web

# Run a second dev instance on a different port
VITE_BACKEND_URL=http://localhost:8082 VITE_DEV_PORT=3001 npm run dev -w web
```

## Build

Build for production (from the repo root, or `cd web` and drop the flag):

```bash
npm run build -w web
```

The built files will be in the `web/dist/` directory and will be served by the HTTP gateway.

## Multiple instances with different backends

The web UI supports talking to multiple gateway backends — either from a
single UI instance (via the Gateway drawer) or by deploying separate UI
instances each pre-configured for a different backend.

### Runtime config (`config.json`)

On startup the SPA fetches `/config.json` (served from `web/public/`).
Override this file per deployment to pre-configure the default backend and
seed additional gateways:

```json
{
  "defaultBackendUrl": "http://gateway-01:8081",
  "gateways": [
    { "host": "gateway-01", "numa": 0, "addr": "gateway-01:8081" },
    { "host": "gateway-02", "numa": 0, "addr": "gateway-02:8081" }
  ]
}
```

- `defaultBackendUrl` — the builtin (non-deletable) gateway's base URL. An
  empty string means same-origin (the gateway serves the SPA itself).
- `gateways` — extra gateway entries that appear in the Gateway drawer on
  first load. Users can switch between them or add more.

If `/config.json` is missing or returns a non-200 response, the SPA falls
back to same-origin defaults.

### Dev mode

Run multiple dev servers, each proxying to a different backend:

```bash
# Instance 1
VITE_BACKEND_URL=http://localhost:8081 VITE_DEV_PORT=3000 npm run dev -w web

# Instance 2
VITE_BACKEND_URL=http://localhost:8082 VITE_DEV_PORT=3001 npm run dev -w web
```

### Production (gateway-served)

When the gateway serves the SPA, each gateway automatically serves a UI
pointing at itself (same-origin). No `config.json` override is needed.

### Production (standalone)

Deploy the built `web/dist/` on a separate static server and place a
custom `config.json` at the root:

```bash
npm run build -w web
# Copy dist/ to your static server
# Place a custom config.json at the server root
```

### CORS

The gateway's HTTP proxy sets `Access-Control-Allow-Origin: *`
(`controlplane/httpproxy/proxy.go`), so cross-origin UI deployments work
without additional configuration. The web client automatically skips gzip
compression for cross-origin requests to avoid CORS preflight issues.
