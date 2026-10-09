#!/usr/bin/env bash
# Stages the Rust lab artifacts into the meson build directory, or removes
# them again.
#
# The functional framework copies every build/modules/*/dataplane/
# *_dp_plugin.so into the guest plugin directory, so a staged plugin
# replaces the built-in decap in any VM booted with plugin_dir until it is
# unstaged. The tagged control plane is staged beside the default one, and
# the boot YAML is generated into the lab manifest directories.
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$poc/../.." && pwd)
build=${YANET_BUILD_DIR:-$root/build}
plugin="$build/modules/decap/dataplane/libdecap_dp_plugin.so"
controlplane="$build/controlplane/yanet-controlplane-rustcp"

case ${1:-} in
    stage)
        (cd "$poc" && cargo build --offline --release -p decap-rs && cargo build --offline --release -p yanet-cp)
        cp "$poc/target/release/libdecap_dp.so" "$plugin"
        (cd "$root" && go build -tags yanet_rust_cp -o "$controlplane" ./controlplane/cmd/yncp-director)
        # The manifests' boot configuration is generated, not committed.
        (cd "$root" && go run rust/sdk-poc-bindgen/lab/gen_config.go)
        echo "staged $plugin, $controlplane and the lab boot configuration"
        ;;
    unstage)
        rm -f "$plugin" "$controlplane"
        echo "removed $plugin and $controlplane"
        ;;
    *)
        echo "usage: $0 stage|unstage" >&2
        exit 2
        ;;
esac
