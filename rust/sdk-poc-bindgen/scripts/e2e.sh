#!/usr/bin/env bash
# Builds the Rust plugin and control-plane library and runs the Go suites
# that link them: the decap functional tests on the dataplane harness with
# the Rust plugin and the Rust api, plus the rure users of the Go link.
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$poc/../.." && pwd)

(cd "$poc" && cargo build --offline --release -p decap-rs && cargo build --offline --release -p yanet-cp)

# The plugin exports exactly the constructor and the ABI version.
plugin="$poc/target/release/libdecap_dp.so"
exports=$(nm -D --defined-only "$plugin" | awk '{print $3}' | grep -v '^_' | sort | tr '\n' ' ')
if [[ $exports != "new_module_decap yanet_module_abi_version " ]]; then
    echo "unexpected plugin exports: $exports" >&2
    exit 1
fi
echo "plugin exports: $exports"

# The control-plane library carries the rure C API and the yanet-cp
# exports, and no dataplane packet helpers that the Go link already has.
lib="$poc/target/release/libyanet_cp.a"
defined=$(nm -g --defined-only "$lib" 2>/dev/null | awk '$2 == "T" {print $3}')
rure=$(grep -c '^rure_' <<<"$defined")
meson_rure=$(nm -g --defined-only "$root/build/lib/counters/librure.a" 2>/dev/null | awk '$2 == "T" && $3 ~ /^rure_/' | wc -l)
echo "rure exports: $rure in libyanet_cp.a, $meson_rure in librure.a"
if [[ $rure -ne $meson_rure ]] || grep -qE '^(packet_decap|parse_packet)$' <<<"$defined"; then
    echo "libyanet_cp.a must carry exactly the rure API and no packet helpers" >&2
    exit 1
fi
grep -E '^yanet_cp_' <<<"$defined" | sort | sed 's/^/control-plane export: /'

cd "$root"
go test -count=1 -tags yanet_rust_cp \
    ./modules/decap/... ./controlplane/ffi/... ./bindings/go/dataplane_ut/...
