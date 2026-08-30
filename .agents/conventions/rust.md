# Rust conventions

Loaded on demand by the agent writing or reviewing Rust; this is the single source for these rules.

- `.rustfmt.toml` uses nightly-only options (`wrap_comments`, `format_code_in_doc_comments`, `imports_granularity`, `group_imports`). Always use `cargo +nightly-2026-08-28 fmt` — the date `rust-check.yml` pins, installed once with `rustup toolchain install nightly-2026-08-28 --component rustfmt`; a newer nightly rewraps comments differently. Bump the date together with a workspace reformat.
- Run `cargo +nightly-2026-08-28 fmt -- --check` and `cargo clippy` before committing.
- **`cargo fmt --all` also formats local path dependencies**, so it reaches outside the crate you think you are formatting. Scope it deliberately when the workspace pulls in path deps from another tree.
- Proto compilation needs `protobuf-compiler` in CI.
- **Proto crates**: tonic-include crates expose `pub mod pb`, never `pub mod <crate>`; consume shared `common/rust/` crates through `extern_path`.
- **Orphan rule**: never implement a foreign trait for a foreign type. Give the CLI a local enum/wrapper, implement the foreign trait there, then `From<Local> for Foreign`; free functions are not a substitute.
- **Visibility**: avoid `pub(crate)`; items are `pub` or private. Conceptual type API methods are `pub`, even in binaries.
- **Wire vs domain types**: parse and check invariants in the domain type; wire types get `From<Domain>` and use `TryFrom` only when fallible. Confirm module-specific validation (ACL permits non-contiguous masks, forward/decap do not) before generalising.
- **`Display`/`Serialize`**: own types implement `Display`; `Serialize` uses `serializer.collect_str(self)`. Never blanket-derive `Serialize` for a proto module with a manual implementation.
- **`--format json` serializes the wire message itself.** A struct mirroring an in-crate `include_proto!` type holds only impls that type could carry — serialize the proto and `impl Tabled`/`Serialize` on it, computed cells and all. A local row is right only where the orphan rule above blocks that, for a wire type from a shared proto crate, or for a document schema that also parses a config file. Fix an unusable derived shape on the wire type via `impl Serialize`/`Display` or a `serialize_with` attribute in `build.rs`, owner-approved as the exception.
- **`fmt` imports**: `use core::fmt::{self, Display, Formatter};` with explicit `Result<(), fmt::Error>` (not `fmt::Result` alias).
- **`clippy::std_instead_of_core`** is deny-level via `[workspace.lints.clippy]` + per-crate `[lints] workspace = true`; prefer `core::` for anything `core` provides (`error::Error`, `fmt`, `net`, `str::FromStr`, `time::Duration`, `mem`, `iter`, `ops`).
- **Escape hatches** are `#[allow(clippy::std_instead_of_core)]` on an enclosing item (function or module), never the `use` itself — clippy's `useless_attribute` rejects that: tonic's client codegen emits `std::` paths we do not control, and `core_io`-gated items (`io::ErrorKind`, `io::Cursor`) are still unstable in `core`.
- **No doc comments** on `Display`/`Serialize`/`TryFrom`/`From`/`Debug`/ `Default`/`FromStr` impls — the trait name is the doc.
- **Comments**: follow `.agents/conventions/comments.md` (brief + blank + detailed), for both `///`/`//!` doc comments and plain `//`. `.rustfmt.toml`'s `wrap_comments` reflows any line past `comment_width` (80 columns) on `cargo +nightly-2026-08-28 fmt`, so keep each line within that width and author the blank-line separator so a reflow never merges it away. Place a comment only where the item is not self-explanatory.
- **No infallible `TryFrom`**: replace with `From`, or remove the impl if the call site is trivially inlinable.
- **`assert_eq!` order**: expected first, actual second: `assert_eq!(expected, actual)`.
- **Style**: prefer shadowing to `_str`, destructure `self` rather than `self.0`, put bounds in `where`, and import types directly.
- **Struct literals**: follow declaration order, including generated protos; rustfmt/clippy do not check this.
- **Empty CLI results**: use `output::empty`/`empty_with_hint`, never bare printing or call-site format guards. The primitive owns the stderr marker, `No <subject> found.` register, and non-TTY/serializing suppression.
- **Tests**: follow `.agents/conventions/tests.md` (naming `fn test_<what_to_test>_<case>`, one-brief `verifies that` doc comment, self-describing `#[case]` names). Reference: `modules/pdump/cli/src/printer.rs`.
