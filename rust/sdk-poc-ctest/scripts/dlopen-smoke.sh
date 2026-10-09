#!/usr/bin/env bash
# Builds the Rust decap plugin and loads it with the dataplane plugin loader.
#
# Needs a configured meson build directory (YANET_BUILD_DIR, default
# <repo>/build) for the DPDK and YANET compile flags.
set -euo pipefail

poc="$(cd "$(dirname "$0")/.." && pwd)"
repo="$(cd "$poc/../.." && pwd)"
build="${YANET_BUILD_DIR:-$repo/build}"
work="$(mktemp -d)"
trap 'rm -r -- "$work"' EXIT

cargo build --release --manifest-path "$poc/Cargo.toml" -p decap-rs
mkdir "$work/plugins"
cp "$poc/target/release/libdecap_dp.so" "$work/plugins/"

# Compile flags of the C decap module, from the meson compilation database.
flags="$(python3 -I - "$build/compile_commands.json" <<'PY'
import json, shlex, sys
for entry in json.load(open(sys.argv[1])):
    if entry["file"].endswith("modules/decap/dataplane/dataplane.c"):
        args = shlex.split(entry["command"])[2:]
        keep, it = [], iter(args)
        for arg in it:
            if arg.startswith(("-I", "-D", "-march")):
                keep.append(arg if not arg.startswith("-I") or arg.startswith("-I/")
                            else "-I" + entry["directory"] + "/" + arg[2:])
            elif arg == "-include":
                keep += [arg, next(it)]
        print(" ".join(shlex.quote(a) for a in keep))
        break
PY
)"
eval "gcc -O2 $flags -rdynamic -o '$work/dlopen_smoke' \
    '$poc/scripts/dlopen_smoke.c' \
    '$repo/lib/dataplane/config/plugin_loader.c' \
    '$repo/lib/logging/log.c' \
    '$repo/lib/dataplane/packet/packet.c' \
    '$repo/lib/dataplane/packet/decap.c' -ldl"
"$work/dlopen_smoke" "$work/plugins"
