#!/usr/bin/env bash
# Builds the decap plugin with the C helpers inlined through LLVM bitcode,
# checks that the inlining happened, and runs the test suite in that mode.
#
# The toolchain pin is rustc 1.88 (LLVM 20) with clang-20 and lld-20; the
# sys build script fails on any LLVM major mismatch. The compile-fail cases
# are skipped: trybuild rebuilds the crate without this build's link flags.
# They do not depend on the C toolchain and run in the default build.
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
toolchain=${BITCODE_TOOLCHAIN:-1.88.0}
cargo=(cargo "+$toolchain" --config "$poc/.cargo/bitcode.toml")
so="$poc/target/bitcode/x86_64-unknown-linux-gnu/release/libdecap_dp.so"
objdump=${LLVM_OBJDUMP:-llvm-objdump-20}

cd "$poc"
"${cargo[@]}" build --offline --release -p decap-rs --features decap-rs/bitcode

# The plugin exports exactly the constructor and the ABI version.
exports=$(nm -D --defined-only "$so" | awk '{print $3}' | grep -v '^_' | sort | tr '\n' ' ')
if [[ $exports != "new_module_decap yanet_module_abi_version " ]]; then
    echo "unexpected plugin exports: $exports" >&2
    exit 1
fi
echo "plugin exports: $exports"

# No out-of-line copy of a C helper is left, and the handler trampoline,
# into which the Rust handler is inlined, calls none of them.
helpers='packet_decap|parse_ipv4_header|parse_ipv6_header'
if nm "$so" | grep -Ew "$helpers"; then
    echo "C helpers left out of line in $so" >&2
    exit 1
fi
handler=$("$objdump" -d --no-show-raw-insn "$so" | awk '/^[0-9a-f]+ <.*decap_dp.*trampoline.*>:$/ {p=1; next} p && /^$/ {exit} p')
if [[ -z $handler ]]; then
    echo "handler trampoline not found in $so" >&2
    exit 1
fi
if grep -E "call.*<($helpers)" <<<"$handler"; then
    echo "the handler calls a C helper" >&2
    exit 1
fi
# Positive evidence: the decap body moved into the handler, so its
# memmove of the Ethernet header is called from there.
memmove_got=$(readelf -rW "$so" | awk '$5 ~ /^memmove@/ {sub(/^0*/, "", $1); print $1}' | paste -sd '|')
if [[ -z $memmove_got ]] || ! grep -qE "call.*# 0x($memmove_got)( |$)" <<<"$handler"; then
    echo "the decap body is not inlined into the handler" >&2
    exit 1
fi
echo "inlined: no call to $helpers, the handler calls memmove itself ($(grep -c . <<<"$handler") instructions)"

"${cargo[@]}" test --offline --release --features decap-rs/bitcode -- --skip test_compile_fail_cases
