#!/usr/bin/env bash
# Builds the decap plugin end-to-end harness against the meson build and
# runs it: the same frames through the built-in C decap and the Rust plugin.
set -euo pipefail

poc=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$poc/../.." && pwd)
build=${YANET_BUILD_DIR:-$root/build}
out="$poc/target/e2e"
mkdir -p "$out/plugins"

(cd "$poc" && cargo build --offline --release -p decap-rs)
cp "$poc/target/release/libdecap_dp.so" "$out/plugins/"

# The plugin exports exactly the constructor and the ABI version.
exports=$(nm -D --defined-only "$out/plugins/libdecap_dp.so" | awk '{print $3}' | grep -v '^_' | sort | tr '\n' ' ')
if [[ $exports != "new_module_decap yanet_module_abi_version " ]]; then
    echo "unexpected plugin exports: $exports" >&2
    exit 1
fi
echo "plugin exports: $exports"

reference=lib/dataplane_ut/tests/dataplane_ut_smoke_test
ninja -C "$build" "$reference" >/dev/null

# Compile with the flags of the smoke test and link with its exact link
# line, swapping in the harness object.
python3 - "$build" "$poc/decap-rs/e2e/decap_plugin_e2e.c" "$out" "$reference" <<'PY'
import json, shlex, subprocess, sys
build, source, out, reference = sys.argv[1:]
db = json.load(open(f"{build}/compile_commands.json"))
entry = next(e for e in db if e["file"].endswith("lib/dataplane_ut/tests/smoke_test.c"))
args = shlex.split(entry["command"])
compile_args, skip = [], False
for arg in args:
    if skip:
        skip = False
        continue
    if arg in ("-o", "-MF", "-MQ", "-c"):
        skip = arg != "-c"
        continue
    if arg == "-MD" or arg.endswith("smoke_test.c"):
        continue
    compile_args.append(arg)
obj = f"{out}/decap_plugin_e2e.o"
subprocess.run(compile_args + ["-c", source, "-o", obj], cwd=entry["directory"], check=True)
commands = subprocess.run(["ninja", "-C", build, "-t", "commands", reference], capture_output=True, text=True, check=True)
link = shlex.split(commands.stdout.strip().splitlines()[-1])
link = [obj if a.endswith("smoke_test.c.o") else a for a in link]
link[link.index("-o") + 1] = f"{out}/decap_plugin_e2e"
subprocess.run(link, cwd=build, check=True)
PY

"$out/decap_plugin_e2e" "$out/plugins"
