# Test conventions

Loaded on demand by the agent writing or reviewing tests in any language; this is the single source for what a test must say about itself. The per-language conventions defer to this file.

Every test names the behaviour it pins and, where the name alone is not enough, carries a one-brief comment stating the invariant under test. A test that checks "it works" checks nothing: name the subject, the case, and the expected outcome.

## Shared rules (all languages)

- **A test must describe what it checks**: the description lives in the name where the name is expressive enough, and in a one-brief doc comment where it is not. A test that checks "it works" checks nothing: name the subject, the case, and the expected outcome. The form is per-language (see below); never both restate the name in a comment and keep the comment — the "No comment restate" rule applies, and a Go comment that opens with the test's own name is its synopsis, not a restate.
- **Doc comment shape (when used)**: opens with `verifies that`/`asserts that` and the invariant — the precondition, the action, the expected outcome — in the same brief shape as `.agents/conventions/comments.md`, including its rule against code identifiers in prose. In Go the test's own name comes first, `Test_<WhatToTest>_<Case> verifies that …`, as any godoc comment opens with its symbol. A detailed block is added only when the invariant is non-obvious (a race, an ordering, a security boundary); most tests need none.
- **Table cases describe themselves**: each `name:`/`#[case]`/`it("…")` field is a self-contained sentence fragment (`"nil range"`, `"family mismatch"`, `"rejects overlong counter"`), never `"case 1"`/`"ok"`/`"error"`. A reader running a filtered run against a failure must know what broke without opening the file.
- **One assertion per invariant**: a table case fails for one reason and the name says which. Bundling unrelated asserts into one case to save lines hides which invariant regressed; split the case.
- **Helpers are documented**: a test helper (`newTestService`, `flakyBackend`, a `run_…_test` scenario, a `create_ipv4_tcp_frame` fixture) gets a one-line brief stating the shape it returns and the invariant it assumes, so a reader does not reverse-engineer the fixture.
- **No comment restate**: a doc comment that paraphrases the function name (`// Test_BlackholeService tests BlackholeService`) is deleted; the comment earns its line by stating what the name cannot. If the name already states the invariant, no comment is added.

## Per-language naming

- **Go**: `Test_<WhatToTest>_<Case>`, CamelCase segments joined by underscores. The first segment is the unit under test (service, type, component), the rest is the case, which may chain multiple CamelCase verbs when the scenario is a sequence (`Test_RouteMPLSService_UpdateConfig_UpdateAndWithdraw`). CamelCase names are terse, so **each test also carries a doc comment opening `// Test_<WhatToTest>_<Case> verifies that …`** stating the invariant the name cannot carry alone; the name comes first as in any Go doc comment. `TestFoo` is rejected at review; rename or split. Reference: `modules/blackhole/controlplane/service_test.go`.
- **Rust**: `fn test_<what_to_test>_<case>`, snake_case, the `test_` prefix mandatory (`test_print_ethernet_frame_concise_ipv4_tcp`, `test_pretty_print_ethernet_frame_malformed`). The snake_case name is expressive enough to be the description; a `/// verifies that` doc comment is added only when the name does not convey the invariant (a race, an ordering, a security boundary). Reference: `modules/pdump/cli/src/printer.rs`.
- **C (`dataplane_ut`)**: one `run_<what>_test(struct yanet_shm *shm)` entry per scenario, dispatched from `main`; the file carries a file-level header comment in the brief + blank + detailed shape stating what regression it pins, and each scenario may open with a one-brief comment on the invariant it drives. Reference: `lib/controlplane/tests/loaded_counts_test.c`.
- **TypeScript (Vitest)**: `describe('<subject>', () => { it('<case in prose>', …) })` — the `it()` string is the case description in plain prose, so no separate doc comment is added; the prose string itself must name the invariant and expected outcome. Reference: `web/src/core/utils/bytes.test.ts`, `web/src/registry.test.ts`.

When in doubt, match the reference for the language you are touching; when a reference falls short, update it and this file together.
