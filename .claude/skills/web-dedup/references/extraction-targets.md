# Extraction targets & coder-ui guardrails

Where each kind of de-duplicated code belongs in `web/src`, and the coder-ui drift to
pre-empt in every extraction brief.

## Where extracted code goes

- **Generic, page-agnostic components** → `web/src/components/`, re-exported from
  `web/src/components/index.ts`. A component's OWN styles colocate next to it as
  `<Component>.tsx` + `<Component>.scss`, imported via `import './<Component>.scss';`.
  Never bury a component's styles in page scss.
- **Generic hooks** → `web/src/hooks/`, re-exported from `web/src/hooks/index.ts`
  (siblings: `usePageKeyboardShortcuts`, `useDialogKeyboardShortcut`,
  `useContainerHeight`, ...).
- **Shared cross-page SCSS** → `web/src/styles/chrome/` partials (`_badges`,
  `_buttons`, `_drawer`, `_forms`, `_modal`, `_surface`), wired through
  `web/src/styles/chrome.scss`.
- **Shared utils / formatters** → `web/src/utils/`. **Shared TS types** → the
  relevant `web/src/api/*` module or a colocated types file; mirror existing exports.

## Hard guardrails (state these in the brief)

- **Never add files under `pages/_shared/`.** It is being dismantled, not expanded.
  Generic components/overlays go to `components/`; generic hooks to `hooks/`. If a
  task would extend an existing `_shared/<x>` module, relocate that module to
  `components/`/`hooks/` rather than growing it in place.
- **Do NOT extract the virtualizer plumbing.** The
  `useVirtualizer` + `getScrollElement` + `useContainerHeight` + `setScrollRef`
  cluster was extracted once (`useVirtualizedTable`) and REVERTED — it returned 0
  visible items (blank pages) despite a correct `getTotalSize()`. Keep it INLINE in
  each table shell (`VirtualTable` / `VirtualDraftTable`).
- **SCSS mixin extraction must preserve source order** and parameterize the single
  diverging value (see verification recipe 1) so the CSS-hash-set proof stays clean.

## coder-ui drift to pre-empt

- **Derived-collection key alignment.** When a filter→map / partition feeds a
  fallback-naming scheme (`item.name ?? \`x-${idx}\``), PRESERVE the original source
  index. A post-filter `idx` silently dropped unnamed plain devices in InstanceCard.
- **Wire types are objects.** `commonpb.MACAddress` / `IPAddress` fields are
  `{ addr: string }`, never a bare `string`. Mistyping crashes React with "Objects
  are not valid as a React child".
- **Barrel integrity.** The barrel (`components/index.ts` / `hooks/index.ts`) must
  reference only files that exist. A dangling `export … from './x'` with no `x.ts`
  ships a broken module graph; the vitest-only web CI stays green and you won't catch
  it until a later build fails. Cross-check every `?? ` untracked file is staged.
- **No double spaces between sentences** in comments; single space only.
- **No banner / section-separator comments** in any file (project hard rule).
