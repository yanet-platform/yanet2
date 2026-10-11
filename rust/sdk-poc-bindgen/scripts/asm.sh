#!/usr/bin/env bash
# Prints the optimised machine code of the resolution and IPv4 lookup
# primitives: Rust probes next to their C twins built with the meson flags.
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$poc/../.." && pwd)
build=${YANET_BUILD_DIR:-$root/build}
out="$poc/target/asm"
mkdir -p "$out"

(cd "$poc" && cargo rustc --offline --release -p yanet-sdk --example asm_probe -- --emit asm -C codegen-units=1 >/dev/null 2>&1)
rust_asm=$(ls -t "$poc"/target/release/examples/asm_probe-*.s | head -1)

flags=$(python3 - "$build" <<'PY'
import json, shlex, sys
db = json.load(open(f"{sys.argv[1]}/compile_commands.json"))
entry = next(e for e in db if e["file"].endswith("modules/decap/dataplane/dataplane.c"))
keep, args, it = [], shlex.split(entry["command"])[1:], None
for i, a in enumerate(args):
    if a.startswith(("-I", "-D", "-march", "-O")) or a in ("-include",) or (i and args[i - 1] == "-include"):
        keep.append(a if not a.startswith("-I") else "-I" + __import__("os").path.normpath(__import__("os").path.join(entry["directory"], a[2:])))
print(" ".join(shlex.quote(k) for k in keep))
PY
)
eval "gcc $flags -S -o $out/asm_probe_c.s $poc/scripts/asm_probe.c"

show() {
    awk -v fn="$2" '$0 ~ "^"fn":" {p=1} p && !/^\s*\.(cfi|p2align|size|type|globl|section|text)/ {print} p && /\.cfi_endproc|^\s*\.size/ {p=0}' "$1"
}
echo "== Rust resolve_ref";  show "$rust_asm" yanet_probe_resolve_ref
echo "== C ADDR_OF_NONNULL"; show "$out/asm_probe_c.s" c_probe_addr_of_nonnull
echo "== Rust IPv4 lookup";  show "$rust_asm" yanet_probe_lookup4
echo "== C IPv4 lookup";     show "$out/asm_probe_c.s" c_probe_lookup4
