# TypeScript/React conventions

Loaded on demand by the agent writing or reviewing TypeScript/React; this is the single source for these rules.

- Prefer arrow function expressions.
- **A co-located spec only runs because `web/vite.config.ts` lists those sibling roots** — vitest's default `include` is web-relative, so a `*.test.ts` added or moved there silently stops running in CI. Diff the collected test-file count on any such move.
- **Browser-visible changes need a real Playwright run on the real path plus an inspected screenshot**; `--list` is not verification. A pixel-diff of two empty states reports a false 0 — assert row counts first.
- **`npm run build -w web` runs vite only and does NOT type-check.** Type errors surface only via `npx tsc --noEmit -p web/tsconfig.json`, so a green build is not a green type-check.
- **Comments**: follow `.agents/conventions/comments.md` (brief + blank + detailed), for both `//` and JSDoc `/** */`.
- **Tests**: follow `.agents/conventions/tests.md` (one-brief `verifies that` doc comment, self-describing `it("…")` strings). A test is `describe('<subject>', () => it('<case in prose>', …))` — subject and case are plain prose, not identifiers. Reference: `web/src/core/utils/bytes.test.ts`, `web/src/registry.test.ts`.
- **User-visible copy — a `yn-field__hint`, tooltip, or placeholder — is a claim, held to the same truth standard as a code comment.** For a route or URL, confirm the path actually exists in the router's route table. Grep the string across the sibling `modules|operators|devices/<name>/web/` roots before trusting it — hint text is copy-pasted between them, so a false or stale one is rarely alone. When your own fix removes the defect a nearby hint or rationale cites as its reason, that copy is now false — grep the symbol you fixed and correct every occurrence naming it.
