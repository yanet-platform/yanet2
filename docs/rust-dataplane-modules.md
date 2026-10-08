# Rust dataplane modules

A module dataplane can be written in Rust with no `unsafe` code of its
own. The SDK lives in three crates under `common/rust/`:

| crate | contents |
| --- | --- |
| `yanet-packet` | network header views (Ethernet, VLAN, ARP, IPv4, IPv6 + extensions, TCP, UDP, ICMP, GRE) and the RFC 1071 checksum, pure and `no_std` |
| `yanet-shm` | read-side mirrors of the shared-memory structures — the offset-pointer protocol, `cp_module`, LPM tries, value tables/lines, the `lib/classify` attribute classifiers and the `lib/filter` signature tree |
| `yanet-dp` | the module ABI: safe `PacketFront`/`Packet`/`Ectx` wrappers, worker services, counters, device routing, and the `define_module!` registration macro |

Every `unsafe` block lives inside `yanet-dp` and `yanet-shm` and carries
its soundness argument; module code consumes only safe APIs. The
reference module `modules/rblackhole` (prefix-match blackhole with a
drop counter) is a complete worked example.

## Writing a module

A module is one handler function plus one macro invocation:

```rust
use yanet_dp::prelude::*;

#[repr(C)]
struct MyConfig {
    cp_module: yanet_shm::CpModule,
    // module fields...
}

// SAFETY: cp_module is the first field, like the C convention.
unsafe impl ModuleConfig for MyConfig {
    fn cp_module(&self) -> &yanet_shm::CpModule {
        &self.cp_module
    }
}

fn handle(ectx: &mut Ectx<'_>, front: &mut PacketFront<'_>) {
    let config = ectx.config::<MyConfig>();
    let mut hits = ectx.counter(config.hits_counter_id).expect("registry");
    while let Some(packet) = front.pop_input() {
        // read headers, look up classifiers, mutate, count...
        if let Some(ip) = packet.ipv4() {
            let dst = ip.dst();
            // decide
        }
        hits.add_packet(packet.len());
        front.output(packet);
    }
}

yanet_dp::define_module! {
    loader: new_module_my,
    name: "my",
    handler: handle,
}
```

The handler contract mirrors the C one: pop every input packet and push
it exactly once to `front.output(...)` or `front.drop_packet(...)`. An
empty front is normal (force-polled ticks). One framework rule matters
in practice: a module running in a device INPUT pipeline has its bare
`output` dropped — the only way a packet leaves is being routed into a
device entry (`route_input`/`route_output` with `set_tx_device_id`),
exactly as the C modules do. Optional macro keys:

- `commit: fn(&DpConfig, &CpModule)` — generation-invariant derivation
  over the config (the C commit handler);
- `commit_ectx: fn(&mut Ectx, &CpModule)` — per-worker derivation into
  the prepared buffer;
- `prepared: T` — declares the per-worker prepared buffer; read it with
  `ectx.prepared::<T>()`.

## What the context gives you

`Ectx` wraps the dataplane's execution context:

- `config::<T>()` — the module's published config for this generation;
- `counter(id)` / `runtime_counter(registry, id)` / the five framework
  counters (`rx_counter()` …) — per-worker, single-writer value arrays,
  size-2 counters are `[packets, bytes]`;
- `device_target(index)` + `route_input/route_output` — send a packet to
  a device entry (set `packet.set_tx_device_id(...)` first, using the
  target's generation device id);
- `object_link(index)` — a linked shared object: `link.object::<T>()`
  for the typed object, `link.counter(id)` for link counters;
- `alloc_packet` / `clone_packet` / `free_packet`, `worker_idx()`,
  `now_ns()`, `recirc_limit()`.

`Packet` exposes the parsed metadata (`network_type`, `network_offset`,
`transport_protocol`, `transport_available`, `hash`, `vlan`,
fragmentation flags), byte access (`data`, `data_mut`, `len`,
`total_len`), typed header views (`ipv4()`, `ipv6_mut()`, `tcp()`,
`udp()`, `icmp()`, `gre()`, `eth()`), geometry edits valid while
unlinked (`strip_head`, `prepend_head`), `decap()` for GRE/IP-in-IP,
`parse()` to re-run the RX parser, and `recirc_try_redirect` (handled
automatically by the routing helpers).

## The control-plane side stays in C

Configs are built and published by the module's C api library
(`modules/<name>/api/controlplane.c`) exactly as for C modules, and the
Go control plane calls it through the usual CGO bindings. The Rust side
only reads: `yanet-shm` mirrors structures byte for byte and reproduces
the C lookups (`lpm4_lookup`, `value_table_get`, `classify_net6_lookup`,
`filter_query`, …). Building or mutating configs from Rust is out of
scope for now.

A module with a Rust dataplane therefore keeps the standard module
layout — `api/` in C, `controlplane/` in Go, `cli/` in Rust — and
replaces only `dataplane/` with a `rust/` crate of
`crate-type = ["staticlib"]`.

## Building and wiring

Rust modules are opt-in, because their cargo build steps resolve a Rust
toolchain and a default or `dataplane_only` build promises to need none
(the same reasoning that keeps the `rure` regex out of the packet path
in `lib/counters/meson.build`):

```sh
meson setup build -Dwith_rust_modules=true
meson compile -C build
meson test -C build 'dataplane_ut_rust*'
```

To add a Rust module:

1. create `modules/<name>/dataplane/config.h` (C, `cp_module` first)
   and the `api/` library that builds it;
2. create `modules/<name>/rust/` with the handler crate, mirroring the
   config struct and registering with `define_module!`;
3. add a `modules/<name>/dataplane/meson.build` modeled on
   `modules/rblackhole/dataplane/meson.build`: a cargo `custom_target`
   producing the static archive, and a `declare_dependency` carrying the
   `new_module_<name>` export flags and `rustc --print
   native-static-libs`;
4. list the crate in the root `Cargo.toml` members;
5. gate the module's `subdir()` in `modules/meson.build` and its dep in
   `dataplane/meson.build` on `with_rust_modules`, and name the module
   in the deployment's `dataplane.yaml` module list to load it.

## ABI safety

The mirrored layouts are pinned three ways:

- compile-time size and offset asserts in the crates, carrying the same
  values as `lib/dataplane/config/plugin_abi_assert.h`;
- `#[no_mangle]` size/offset getters (`yanet_dp_sizeof_*`,
  `yanet_dp_offset_*`, `yanet_dp_abi_version`) cross-checked against the
  real headers and `YANET_MODULE_ABI_VERSION` by the `dataplane_ut_rust_abi`
  test in the same binary;
- the `yanet_module_abi_version` a plugin build exports (the `plugin`
  cargo feature), which the plugin loader checks at load.

The version symbol is feature-gated because the statically linked
built-ins must never define it: two Rust modules linked into the same
dataplane binary would collide on it, the same reason the C side keeps
its `plugin_abi_export.c` out of the static module libraries. The
module SDK's pkg-config file also exports
`pkg-config --variable=yanet_abi_version yanet-module-sdk` for
out-of-tree builds to assert against (see docs/module-sdk.md).

`YANET_MODULE_ABI_VERSION` bumps must update `yanet-dp`'s mirrors in the
same change.

## Panics and error handling

Handlers have no error path: release builds use the workspace's
`panic = "abort"` profile, so a panic aborts the dataplane exactly like
a C crash, and no unwind ever crosses the FFI boundary. Code that can
fail (short headers, missing counters, exhausted headroom) returns
`Option`/`bool` instead — decide between passing and dropping the
packet.

## Limitations

- Config building, classifier compilation and the Go/CLI control plane
  remain in C/Go/Rust-CLI respectively; the SDK is the dataplane read
  side.
- in-tree Rust modules link statically into the `yanet-dataplane`
  binary; the out-of-tree plugin path (a `cdylib` built with
  `--features yanet-dp/plugin`, deployed via `plugin_dir` like a C
  plugin) is demonstrated by `sdk/example-rs` and docs/module-sdk.md.
- Multi-segment mbufs: header views and lengths cover the first segment
  (matching the parser's cached metadata); `total_len()` reports the
  whole packet.
