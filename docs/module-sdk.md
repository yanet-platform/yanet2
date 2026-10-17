# Module SDK: building modules out of the yanet2 tree

The module SDK lets a YANET module — dataplane plugin, control-plane
daemon, CLI — live in its own repository and build against a yanet2
checkout, without editing any tracked yanet2 file and without forking the
tree. `sdk/example/` is a complete reference module built this way; copy it
to start a new one. The `external/route-mpls` submodule is the
production-grade port of a real module — see
[yanet-module-route-mpls](https://github.com/yanet-platform/yanet-module-route-mpls).

The dataplane plugin can be written in C (everything above) or in Rust
through the Rust dataplane SDK (`yanet-packet`/`yanet-shm`/`yanet-dp`,
documented in [rust-dataplane-modules.md](rust-dataplane-modules.md)):
`sdk/example-rs/` is sdk/example's twin with a Rust handler. A Rust
plugin builds as a cargo `cdylib` with `--features yanet-dp/plugin` —
the feature emits the `yanet_module_abi_version` export the loader
dlsym's, which statically linked built-ins must never define — and its
undefined C symbols resolve against the dataplane binary exactly like a
C plugin's. The control plane, daemon and CLI are identical to the C
frontend's.

The runtime machinery is part of the dataplane itself: it scans a
configured `plugin_dir` for `lib<name>_dp.so` files, refuses any whose
exported `yanet_module_abi_version` does not match, and prefers a plugin's
`new_module_<name>` constructor over the statically linked built-ins. The
SDK adds the missing build-side half: the include paths and defines of a
configured build, and the archive that stamps a plugin with its ABI
version.

## Quickstart

```sh
# once, in the yanet2 checkout (this is the SDK):
git submodule update --init
meson setup build && meson compile -C build   # or: make sdk

# per module build, in the module's own tree:
cp -r sdk/example ~/my-module && cd ~/my-module
# adjust: module name in dataplane/api sources, go.mod module path,
# the go.mod replace directive and cli/ path deps -> your yanet2 checkout
make YANET_ROOT=/path/to/yanet2 test
```

`make YANET_ROOT=... all` produces, inside the module's own `build/`:

- `lib<name>_dp.so` — the dataplane plugin, deployed to the dataplane's
  `plugin_dir`;
- `lib<name>_cp.a` — the control-plane FFI archive linked by the module's
  Go bindings;
- the control-plane daemon binary and the `yanet-cli-<name>` CLI.

The module SDK's pkg-config metadata lives at
`<yanet2>/build/sdk/yanet-module-sdk.pc`; the reference Makefile points
`PKG_CONFIG_PATH` at `<yanet2>/build/sdk`, which is all a meson project
needs to consume it (`dependency('yanet-module-sdk')`).

## The dataplane plugin

A plugin is a `shared_module` linking only the SDK dependency:

```meson
project('yanet-module-mine', 'c', meson_version: '>= 0.61')
yanet_sdk = dependency('yanet-module-sdk')
shared_module('mine_dp', files('dataplane/dataplane.c'),
              dependencies: [yanet_sdk])
```

The C contract is one exported constructor returning a `struct module`
descriptor (see `lib/dataplane/module/module.h` and
`sdk/example/dataplane/`):

```c
struct module *
new_module_mine() {
	struct module *module = malloc(sizeof(*module));
	memset(module, 0, sizeof(*module));
	snprintf(module->name, sizeof(module->name), "%s", "mine");
	module->handler = mine_handle_packets;
	module->commit_handler = mine_module_commit;
	return module;
}
```

Undefined references to yanet and DPDK symbols are intentional: they are
left unresolved in the `.so` and satisfied by the dataplane binary's
exported dynamic symbols when the plugin is `dlopen`ed. Do not link yanet
libraries into a plugin — a second copy of any stateful library in the
same process is a bug, not a feature.

### ABI discipline

- `lib/dataplane/module/module.h` defines `YANET_MODULE_ABI_VERSION`; the
  SDK dependency links an archive exporting
  `yanet_module_abi_version = YANET_MODULE_ABI_VERSION` into every plugin
  (with `--whole-archive`, since nothing references it at link time).
- The loader refuses a plugin whose version differs from the running
  dataplane. A version bump in yanet2 means every plugin must be rebuilt.
- `lib/dataplane/config/plugin_abi_assert.h` compiles size tripwires for
  the boundary structs into both sides, so most layout changes fail the
  build rather than the deployment.
- A plugin must stick to the lib surface the dataplane binary actually
  links (the packet/module/config headers used by in-tree modules). A
  symbol that no in-tree code references may be dead-stripped from the
  binary; the failure mode is a loud `dlopen ... undefined symbol` at
  startup.

### Deployment

In `dataplane.yaml`, point `plugin_dir` at the plugin directory and name
the module so the loader runs its constructor (the `modules:` spelling
uses underscores, cf. `route_mpls`):

```yaml
dataplane:
  plugin_dir: /usr/lib/yanet2/modules
  modules:
    - example
```

A plugin present in `plugin_dir` but broken aborts startup; the plugin
mechanism never fails silently.

## The control plane

An out-of-tree module does not compile into `yncp-director` (its module
list, `controlplane/bundle/`, is static by design). It runs as its own
daemon instead:

- its Go module has its own `go.mod` with
  `replace github.com/yanet-platform/yanet2 => /path/to/yanet2`, so the
  CGO packages (`controlplane/ffi`) build against the checkout's
  `build/` archives — their `#cgo` paths are relative to the yanet2 tree,
  not to the consumer;
- it attaches to the dataplane shared memory with `ffi.Attach` and
  publishes configs exactly like an in-tree module (`sdk/example/controlplane/`);
- it serves its gRPC service on its own endpoint and heartbeats itself
  into the gateway's backend registry via the gateway `Register` RPC
  (`sdk/example/cmd/example-controlplane/`), so CLIs routed through the
  gateway reach it with no director involvement.

Sample daemon configuration: `sdk/example/etc/example-controlplane.yaml`.

## The CLI

A module CLI is a standalone cargo workspace (the yanet2 workspace
deliberately keeps an explicit member list), consuming the shared CLI core
by path:

```toml
[workspace]

[dependencies]
ync = { path = "/path/to/yanet2/cli/core", package = "yanet-cli" }
```

`build.rs` compiles the module's proto with the yanet2 root as the proto
include directory, mapping shared imports onto the prebuilt
`yanet-commonpb`/`yanet-filterpb` crates (see `sdk/example/cli/`). Build
it with `CARGO_TARGET_DIR` pointing at the checkout's `target/` so the
binary lands next to the in-tree CLIs; private module CLIs are not part
of the public deb.

## Testing

The in-process dataplane harness loads plugins too:
`dataplaneut.Config{PluginDir, Modules: []string{"<name>"}}` boots the
module's `.so` inside a Go test, and the module's own bindings publish
the config — `sdk/example/tests/functional/` wires a full
chain → pipeline → device round trip this way.

## Private in-tree modules

A module may also live under `modules/` without being tracked: any
directory there outside the public whitelist is picked up automatically
(`extra_modules`) and its CLI built as a standalone workspace. Building
its dataplane as a `shared_module` (instead of a static library, adding
`lib_module_abi_export_dep` to its dependencies) keeps it out of the
hardcoded `yanet-dataplane` link list entirely — the private module then
needs zero edits to tracked build files. The fully external layout above
is preferred when the module has its own repository.

## The ported route-mpls module

[yanet-module-route-mpls](https://github.com/yanet-platform/yanet-module-route-mpls)
is a real module ported to this shape: its C sources are verbatim from
`modules/route-mpls`, the Go control plane and CLI carry only the
import-path moves, and the functional suite loads the plugin into the
in-process harness. yanet2 carries it as the `external/route-mpls`
submodule (so all its relative paths — the go.mod `replace`, the CLI's
`ync` dependency, the CGO include paths — resolve by default) and gates
it alongside `sdk/example` in `make sdk-example` when the submodule is
checked out. The in-tree module remains the shipped implementation; the
port exists to prove and pressure-test the SDK with production code.

## Limits

- Devices (`devices/`) cannot be plugins: `dp_load_device` resolves
  constructors only in the main binary.
- No web UI registration for out-of-tree modules: `web/` discovers module
  UIs by globbing its own tree.
- The SDK is build-directory based, not installable: the pkg-config
  metadata embeds absolute paths of one configured build, and headers must
  match the running dataplane (regenerate after moving a tree).
- Fuzzing targets are a meson-internal registry; out-of-tree modules run
  their own harnesses (the functional harness is the supported path).
