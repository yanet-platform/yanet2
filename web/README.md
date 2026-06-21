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

The development server will run on `http://localhost:3000` and proxy API requests to the backend.

## Build

Build for production (from the repo root, or `cd web` and drop the flag):

```bash
npm run build -w web
```

The built files will be in the `web/dist/` directory and will be served by the HTTP gateway.
