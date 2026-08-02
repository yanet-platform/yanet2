# TypeScript/React conventions

Loaded on demand by the agent writing or reviewing TypeScript/React; this is the single source for these rules.

- Prefer arrow function expressions.
- **A co-located spec only runs because `web/vite.config.ts` lists those sibling roots** — vitest's default `include` is web-relative, so a `*.test.ts` added or moved there silently stops running in CI. Diff the collected test-file count on any such move.
- **Browser-visible changes need a real Playwright run on the real path plus an inspected screenshot**; `--list` is not verification. A pixel-diff of two empty states reports a false 0 — assert row counts first.
- **`npm run build -w web` runs vite only and does NOT type-check.** Type errors surface only via `npx tsc --noEmit -p web/tsconfig.json`, so a green build is not a green type-check.
