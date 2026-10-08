#!/usr/bin/env bash
# Prints the optimised x86-64 assembly of the Rust resolver and lookup next to
# the C ADDR_OF macros and lpm_lookup, both for -march=haswell.
set -euo pipefail

poc="$(cd "$(dirname "$0")/.." && pwd)"
repo="$(cd "$poc/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -r -- "$work"' EXIT

gcc -O2 -march=haswell -D_GNU_SOURCE -I"$repo" -S -fno-asynchronous-unwind-tables \
    -o "$work/c.s" "$poc/scripts/asm_probe.c"
RUSTFLAGS="-C target-cpu=haswell" cargo rustc --quiet --release --manifest-path "$poc/Cargo.toml" \
    -p yanet-sys --example asm_probe -- --emit "asm=$work/rust.s" -C debuginfo=0

# Prints one function body without directives.
body() {
    awk -v fn="$2" '$0 == fn":" {p=1; next} p && /^[[:space:]]*\.(cfi_endproc|size)/ {exit}
        p && !/^[[:space:]]*\./ && !/^\.L/ {print}' "$1"
}
for pair in "c_resolve rust_resolve" "c_resolve_nonnull rust_resolve_nonnull" "c_lookup6 rust_lookup6"; do
    set -- $pair
    echo "=== $1"; body "$work/c.s" "$1"
    echo "=== $2"; body "$work/rust.s" "$2"
done
