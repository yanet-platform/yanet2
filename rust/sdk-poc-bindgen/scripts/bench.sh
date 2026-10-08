#!/usr/bin/env bash
# Benchmarks the decap handler: the C module against the Rust plugin built
# four ways, then the LPM lookup alone, pinned to one CPU.
#
# Every Rust build uses -Ctarget-cpu=haswell, the meson -march, and rustc
# 1.88, the bitcode pin, except bindgen-stable, which shows what a newer
# rustc does without LTO. The C module and its helpers are the gcc build
# with the meson flags. Arguments go to the handler benchmark (--front,
# --passes, --samples); BENCH_CPU picks the CPU (default 2).
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
toolchain=${BITCODE_TOOLCHAIN:-1.88.0}
cpu=${BENCH_CPU:-2}
out="$poc/target/bench"
cd "$poc"

stable=${STABLE_TOOLCHAIN:-1.98.0}

build() {
    local dir=$1
    shift
    RUSTFLAGS=-Ctarget-cpu=haswell CARGO_TARGET_DIR="$out/$dir" cargo "+$toolchain" build --offline --release "$@"
}

# bindgen: the default build, C helpers called out of line.
build bindgen -p decap-rs
RUSTFLAGS=-Ctarget-cpu=haswell CARGO_TARGET_DIR="$out/bindgen-stable" \
    cargo "+$stable" build --offline --release -p decap-rs
# bindgen-lto: the same with whole-program Rust LTO, which inlines across
# Rust crates but cannot see into C. Cargo skips LTO for a crate that is
# also an rlib, so the library is built as a cdylib only.
RUSTFLAGS=-Ctarget-cpu=haswell CARGO_PROFILE_RELEASE_LTO=fat CARGO_TARGET_DIR="$out/bindgen-lto" \
    cargo "+$toolchain" rustc --offline --release -p decap-rs --lib --crate-type cdylib
# bitcode: C helpers inlined into the Rust handler.
cargo "+$toolchain" --config .cargo/bitcode.toml build --offline --release -p decap-rs --features decap-rs/bitcode
# The driver links the C module from the oracle: gcc, meson flags.
build driver -p decap-bench

echo "== decap handler (CPU $cpu)"
taskset -c "$cpu" "$out/driver/release/decap-bench" "$@" \
    "bindgen=$out/bindgen/release/libdecap_dp.so" \
    "bindgen-stable=$out/bindgen-stable/release/libdecap_dp.so" \
    "bindgen-lto=$out/bindgen-lto/release/libdecap_dp.so" \
    "bitcode=$poc/target/bitcode/x86_64-unknown-linux-gnu/release/libdecap_dp.so"

echo "== LPM lookup, default build: C helper from gcc (CPU $cpu)"
build lookup -p yanet-sdk --example lookup_bench
taskset -c "$cpu" "$out/lookup/release/examples/lookup_bench"
echo "== LPM lookup, bitcode build: C helper from clang-20 (CPU $cpu)"
cargo "+$toolchain" --config .cargo/bitcode.toml build --offline --release -p yanet-sdk --features yanet-sdk/bitcode \
    --example lookup_bench
taskset -c "$cpu" "$poc/target/bitcode/x86_64-unknown-linux-gnu/release/examples/lookup_bench"
