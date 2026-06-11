# Zero-visual-diff proof recipes

The web-dedup gate is mandatory: an extraction merges only if it passes the proof
that fits its change type. If none yields a clean result, **defer** the candidate.

All `dist` builds: from `web/`, `npm run build` (output `dist/`, hashed chunks under
`dist/assets/`). Baseline = a clean `origin/main` checkout/worktree; candidate = the
extraction worktree.

## 1. SCSS-mixin / presentation-only → CSS-hash-set

Use when the change only moves/rewrites styles (extracting a shared mixin or chrome
partial) and emits the same CSS.

Build `dist` in both the clean baseline and the candidate worktree, then compare the
SET of emitted-CSS sha256 (run from each web dir):

```
find dist -name '*.css' -exec sha256sum {} \; | awk '{print $1}' | sort
```

Identical sets ⇒ byte-identical CSS ⇒ zero diff. No backend or Playwright needed.

Requirements to keep this clean:
- Preserve source ORDER. Interleaved page-specific blocks must become granular
  order-preserving mixins, not one omnibus mixin.
- Parameterize the ONE diverging value (e.g. `$pad-v`, or `$thumb-radius: null`
  emitted only via `@if`), so the mixin reproduces each call site exactly.

## 2. Type-only / pure-logic-no-render → dist JS+CSS hash

Use for type-only changes or pure logic that does not feed rendered output.

Hash ALL `dist` JS + CSS in baseline and candidate:

```
find dist -name '*.js' -o -name '*.css' | sort | xargs sha256sum | awk '{print $1}' | sort
```

Identical ⇒ zero runtime change. (File names are content-hashed, so compare the set
of content hashes, not the names.)

## 3. Logic feeding rendered values (formatters) → exhaustive Node sweep

Use when the extracted logic computes a value that gets rendered (a formatter,
parser, classifier). Hashes won't help because the bundle legitimately changes.

Write a throwaway Node script that runs the OLD impl and the NEW impl over:
- every tier boundary / branch threshold the formatter has, and
- a large batch of random inputs.

Assert old-output === new-output for all samples. A ~5000-sample sweep is the floor;
that scale once caught a `formatPps` divergence at the "G" tier boundary that a few
hand-picked cases missed. If the logic also renders, pair this with recipe 4.

## 4. Visual / JSX → Playwright pixelmatch

Use for component/JSX extraction where output is visual.

Setup (both servers need the Go gateway on `:8081`; vite proxies `/api` →
`http://localhost:8081`, dev port default 3000):
- Run baseline `npm run dev` and candidate `npm run dev` on two different ports.
- coder-ui drives Playwright + pixelmatch and reports per-route diff-pixel counts.
- **You (architect) Read the saved PNGs** — do not trust the count alone.

Caveats:
- Pixel-diff only exercises the DEFAULT state of each route. A diff of two empty /
  no-data states reports a FALSE 0 and hides total render breakage. Before trusting a
  per-route 0, assert the table actually rendered rows (row-selector count > 0) in
  BOTH baseline and candidate; flag data-empty routes as skipped.
- Pixel-diff does NOT cover the dirty / error / added / changed / removed branches —
  have `reviewer` prove those by reading source.
- Known visual-noise routes (nonzero even baseline-vs-baseline):
  `/builtin/dashboard`, `/builtin/inspect`, `/operators/route`. Small diffs on ONLY
  these are noise; any OTHER route must be 0.
- Playwright + Chromium + pixelmatch + pngjs are pre-cached under `web/node_modules`
  and `~/.cache/ms-playwright`. They are NOT in `package.json` — never add them; tell
  coder-ui to revert any manifest/lockfile change before reporting.
