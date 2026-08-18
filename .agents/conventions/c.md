# C conventions

Loaded on demand by the agent writing or reviewing C; this is the single source for these rules.

- Always use braces for `if`/`else`/`for`/`while`, even single-line bodies. `clang-format` (`InsertBraces: true`) inserts them automatically, but it silently skips some cases, among them a body inside a macro definition, a body split from its condition by a preprocessor directive, and an empty `;` body.
- Format with `clang-format`, restricted to the exact `.c`/`.h` paths you changed. Never pass a `.go` or `meson.build` path (alone or batched with C files) — clang-format has no concept of Go or meson grammar and silently mangles it with no warning (breaks `:=`, `for range`, `key:` syntax). Format Go separately with `gofmt`.
- **Comments**: follow `.agents/conventions/comments.md` (brief + blank + detailed); `.clang-format`'s default `ReflowComments` wraps anything past `ColumnLimit` (80 columns) on `clang-format -i`, so keep each line within that width and author the blank-line separator so a reflow never merges it away. Place a comment only where the code is not self-explanatory.
- **Functions with more than six parameters are a code smell.** Split them or use a designated-initializer config struct; omnibus initialisers are untestable.
- **Multi-segment mbufs**: `rte_pktmbuf_data_len()` is head-only. Whole-packet work walks `mbuf->next`/uses `rte_pktmbuf_pkt_len()`, or rejects chained packets.
- **Zero config limits mean unset/no clamp.** Never use the sentinel in min/subtraction arithmetic; clamp accepted degenerate values below internal header deltas.
- **Write recycled wire fields at their declared width.** A partial write retains stale bytes.
- **`memory_balloc` does not zero.** `memset` allocations whenever a sentinel, CGO-visible field, or index depends on zero/NULL.
- **cgo-boundary headers stay DPDK-free.** `<rte_*.h>` in `packet.h`, `packet_front.h`, `lib/utils/packet.h`, or another cgo-path header is blocking.
- **Packet data access checks `rte_pktmbuf_data_len` before reading.** An unchecked read past the buffer is a security-critical overflow.
- **`cp_module` MUST be the first field of every config struct.** `container_of()` correctness depends on it.
- **`container_of()` must be used correctly** — the correct struct type and the correct member name.
- **`memory_balloc` MUST have a corresponding `memory_bfree` in cleanup paths.**
- **`dataplane_ut` `Bench`/`run_rounds` recycles fixed packets**: neutralise allocating or emitting actions (for example ACL `CREATE_STATE` sync) so mbufs do not leak or distort results.
- **Tests**: follow `.agents/conventions/tests.md` (one-brief `verifies that` doc comment, self-describing scenario names). A `dataplane_ut` file uses one `run_<what>_test(...)` entry per scenario dispatched from `main`, plus a file-level header comment in the brief + blank + detailed shape. Reference: `lib/controlplane/tests/loaded_counts_test.c`.
