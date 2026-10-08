# vxlan: builtin Rust device (proof of concept)

A VXLAN device type written in safe Rust and statically linked into
`yanet-dataplane` through one umbrella Rust staticlib. The plugin loader is
unchanged: the dataplane resolves `new_device_vxlan` from its own binary with
`dlsym(dlopen(NULL))`, exactly as it does for `plain`, `vlan` and `trafgen`.

Scope: dataplane only, IPv4 underlay only. There is no Go control plane, CLI or
web page; a C api creates devices for the C test.

## Layout

```
lib/rust/                       dataplane Rust workspace (separate from the CLI one)
  Cargo.toml, Cargo.lock        members: sys, umbrella, devices/vxlan/dataplane
  meson.build                   shim static library + cargo custom_target
  cargo-staticlib.sh            runs cargo, copies the archive, rewrites dep-info
  yanet-dp-sys/                 the only crate with unsafe code
    build.rs, wrapper.h         bindgen over repository headers at build time
    shim/yanet_dp_shim.{c,h}    out-of-line wrappers of inline C/DPDK helpers
    src/lib.rs                  Device trait, Packet, relative pointers, export macro
    src/tests.rs                strict-provenance tests run under Miri
  yanet-dp-builtins/            umbrella staticlib, the only Rust archive linked
devices/vxlan/
  dataplane/config.h            C body layout: cp_device_vxlan = cp_device + body
  dataplane/Cargo.toml, src/    device crate, #![no_std] #![forbid(unsafe_code)]
  dataplane/meson.build         defsym/export flags for new_device_vxlan
  api/                          C creation path (mirrors devices/vlan/api)
  tests/device_test.c           dataplane_ut test through the real loader
```

## Architecture

- `yanet-dp-sys` runs bindgen at build time over `wrapper.h` (device ABI,
  `device_ectx`, `packet`, `cp_device`, the vxlan body and the shim). The
  common device header, `packet_front`, `dp_worker` and `dp_config` are
  generated opaque: Rust gets their size and alignment and no fields.
- The device crate implements `yanet_dp_sys::Device`:
  `input/output(&Config, &mut Packet) -> Verdict`, with two associated types.
  `Body` is the C body; it must implement the sealed `DeviceBody` trait, which
  only sys implements (here for the bindgen type `vxlan_device_config`) and
  which carries the C type name `"vxlan"` and the body offset
  `offsetof(cp_device_vxlan, config)`. `Config` is the Rust view; it must be
  `zerocopy::FromBytes + Immutable + Sync` (any bytes valid, no interior
  mutability, shared by all workers), and sys fails the build at
  monomorphization unless it is no larger and no more strictly aligned than
  `Body`. Layout facts every body read relies on therefore live in sys, not in
  the safe device crate; the device crate additionally asserts its view field
  by field against bindgen for correctness.
- `export_device!(vxlan, Vxlan)` accepts only an identifier and a path. It
  fails the build unless the identifier equals the body's C type name, and
  emits `#[unsafe(export_name = "new_device_vxlan")] extern "C" fn` calling the
  safe generic `new_device::<Vxlan>`. That function fills `struct device`
  (name from the body) with monomorphized `unsafe extern "C"` handlers living
  in sys. The descriptor is allocated with C `malloc` because
  `dp_load_device` releases it with `free`.
- The generic handler resolves the device body once per invocation:
  `device_ectx->cp_device` is a relative slot; the target is
  `ectx.with_addr(slot_addr + offset)`, so it keeps the provenance of the raw
  pointer C passed in, never of a Rust reference, with no exposed provenance.
  The view is read at the C body offset past the header; no reference covers
  the header (registry item, memory context). It then pops
  every input packet through the shim, hands it to the device as a `Packet`
  and links it to output or drop by the returned verdict, so a device cannot
  lose a packet.
- `Packet` exposes the first segment as `&[u8]`/`&mut [u8]`, the whole length,
  the parser hash, `prepend`, `trim_front` and `reparse`; all are shim calls.
- `yanet-dp-builtins` is `#![no_std]`, has the panic handler (C `abort`) and
  depends on every builtin Rust item. Profiles use `panic = "abort"`; release
  adds fat LTO and one codegen unit.
- Meson: `lib/rust/meson.build` builds the shim and the archive (cargo dep-info
  becomes the ninja depfile, so Rust sources and bound headers trigger
  rebuilds and a no-op build does not run cargo). `lib_dev_vxlan_dp_dep` adds
  `--defsym new_device_vxlan` and `--export-dynamic-symbol=new_device_vxlan`,
  like `vlan`. `dataplane.c` appends `"vxlan"` to `devices[]` under
  `YANET_RUST_BUILTINS`. Option `-Ddataplane_rust=auto|enabled|disabled`
  (default auto: on when cargo is found).

## Device behaviour

- Output (RFC 7348 section 5): drops frames shorter than an Ethernet header;
  prepends 50 bytes of outer Ethernet (local to remote MAC), IPv4 (local to
  remote, DF, ID 0, TTL 64, header checksum), UDP (destination 4789, checksum
  zero as the RFC recommends over IPv4, source port
  `49152 | ((hash ^ hash >> 16) & 0x3fff)`) and VXLAN (flags 0x08, reserved
  bits zero, VNI); drops when the result would exceed an IPv4 datagram or the
  headroom is too small; re-parses so later stages see the outer offsets.
- Source port entropy: `packet->hash` is the parser's crc32 over the inner
  addresses and L4 ports (`lib/dataplane/packet/packet.c`), which is the
  inner-flow hash the RFC recommends, mapped into the RFC 6335 dynamic range.
  A frame the parser hashes nothing for (for example ARP) has hash 0 and
  always uses port 49152.
- Input: only an unfragmented IPv4 UDP datagram to the local address and port
  4789 is tunnel traffic. With the configured VNI, the I flag set and a whole
  inner Ethernet header, the outer stack (IPv4 options honoured) is stripped
  and the inner frame re-parsed. A zero UDP checksum is accepted and the
  reserved bits are ignored, as the RFC requires; a non-zero checksum is not
  verified. Tunnel traffic for the local
  address with another VNI, without the I flag or truncated is dropped.
  Everything else passes unchanged, because the underlay port also carries
  the router's own traffic. Outer fragments pass (no reassembly).
- Classification reads the first segment only: a tunnel packet whose UDP,
  VXLAN or inner Ethernet header crosses into a second segment is dropped,
  and one whose outer IPv4 header is split passes undecapsulated. The outer
  IPv4 total length and UDP length are not checked against the frame, so
  trailing bytes after the datagram stay attached to the inner frame.
- The C api refuses VNIs wider than 24 bits; Rust masks the VNI as well.

## Build and run

```
git submodule update --init
PKG_CONFIG=/usr/bin/pkg-config meson setup build      # this host: nix pkg-config misses yaml-0.1
LIBCLANG_PATH=/usr/lib/llvm-18/lib meson compile -C build
meson test -C build vxlan_device
nm -D build/dataplane/yanet-dataplane | grep new_device_vxlan

cd lib/rust
LIBCLANG_PATH=/usr/lib/llvm-18/lib cargo +1.88 test --workspace
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test --workspace
MIRIFLAGS="-Zmiri-strict-provenance -Zmiri-tree-borrows" cargo +nightly miri test --workspace
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-dp-sys -- --ignored   # must fail
```

## What is proven

- `nm -D build/dataplane/yanet-dataplane` lists `T new_device_vxlan` next to
  the C devices and no other Rust symbol. The full symbol table has no
  `rust_eh_personality`, `__rdl_*` or other runtime symbol and only three
  local (`t`) Rust symbols, the generic handlers: fat LTO internalizes
  everything, and the LTO object (1653 bytes of text) defines only
  `new_device_vxlan` and imports only `malloc` and the eight shim functions. The shim functions are C globals
  and are exported like every other C global of the `-E` binary.
- `vxlan_device_test` (dataplane_ut) loads `plain` and `vxlan` through
  `dp_load_device` from the test binary, which links the archive with the same
  flags as `yanet-dataplane`. Five scenarios: the constructor registers all
  handlers; a VXLAN packet with the device VNI is decapsulated on input and
  re-encapsulated on output with the configured outer headers (checksum,
  port range, VNI, inner bytes checked); another VNI drops on input; a plain
  frame passes input and is encapsulated on output; the C api refuses a 25-bit
  VNI. A mutation (decap returning pass) makes the test fail.
- Device unit tests (24, byte buffers): round trip, exact outer wire format,
  IPv4 checksum, zero UDP checksum, zero reserved VXLAN bits, oversize, VNI
  masking, source port range and zero hash, VNI mismatch, missing I flag, any
  UDP checksum and set reserved bits accepted on receipt, truncated VXLAN
  header, truncated inner frame, runt, truncated IPv4, non-UDP, other port,
  other destination, non-IPv4, fragment, IPv4 options.
- Layout enforcement in sys: a forbid(unsafe_code) crate exporting a device
  whose view is `[u8; 4096]` over the vxlan body fails to compile ("the
  device config view must fit inside its C body"), and exporting it under the
  name `vlan` fails ("the exported name must be the type name of the device
  body"). Both are pinned by `compile_fail` doctests on the export macro.
- Miri with `-Zmiri-strict-provenance`, Stacked and Tree Borrows: body
  resolution through the relative slot, null slot, backward offset, descriptor
  allocation, handler entry on an empty front. All pass in both models.
- Negative: resolving from a root narrowed to a reference to the context is
  reported as undefined behaviour by Stacked Borrows and accepted by Tree
  Borrows; the strict-provenance rule therefore has to be upheld by the API
  shape, Miri catches its violation only in Stacked Borrows.
- `#![forbid(unsafe_code)]` holds in the device crate with the export macro
  expanded inside it.

## Numbers

Lines (wc -l): sys `lib.rs` 426 (50 of them doctests), sys tests 296, `build.rs` 43, `wrapper.h` 12,
shim 58 + 54, umbrella 19, `lib/rust` meson/script/manifest 45 + 25 + 37;
device crate 299 + tests 245 (one `unsafe` token: the forbid attribute);
C body header 29, C api 140 + 63, C test 532.

Build times on this host (warm ccache for C):
- Clean `cargo +1.88 build --release` of the umbrella: 10.9 s (bindgen,
  zerocopy-derive, fat LTO).
- `ninja dataplane/yanet-dataplane` in a fresh build dir where only the Rust
  part is new: 17.2 s including the relink.
- Incremental meson build after touching the device crate: 2.7 s.
- No-op meson build: cargo is not run.

Archive: 4.2 MB, almost all `compiler_builtins`, which the link does not pull.

## Friction and where the design did not hold

- `dp_load_device` calls `free` on the descriptor, so sys must call C
  `malloc`; a `static` descriptor would be freed.
- `packet_front` and mbuf helpers are static inline C or DPDK macros: Rust
  reaches them through an 8-function C shim, one out-of-line call per
  operation per packet. No cross-language LTO; throughput is not measured.
- Packet metadata: decap and encap call the C parser again instead of
  patching offsets as the C decap does. Correct, but it costs a parse per
  packet in both directions.
- The device body layout is owned by the C header. A safe device crate
  cannot be trusted with it (a larger view would read out of bounds), so sys
  must know every builtin body: the bindgen input and the sealed body trait
  both list the vxlan body. That keeps soundness in sys but does not scale;
  production needs per-item layout crates owned by the trusted side, or
  Rust-defined bodies with generated C headers.
- The device body reference is valid only under the "frozen after publish"
  convention; nothing enforces it at runtime.
- `forbid(unsafe_code)` does not see unsafe code produced by a macro from
  another crate. That is what lets the export macro work, and it also means
  the lint does not audit the macro output: the macro must stay in the
  trusted sys crate and take only identifiers and paths.
- The narrow-root misuse is detected only by Stacked Borrows (above). The
  Miri fixture is a Rust allocation laid out like the control plane does it,
  not a C-produced shared-memory image.
- Topology: the initial generation gives every port a `plain` device, and a
  topology device cannot be replaced. A vxlan device is therefore a logical
  device that sees only traffic a module routes to it; in the test a forward
  rule hands port traffic to the vxlan input, and a second one hands decapped
  packets to the vxlan output. A deployment needs such rules (or a port-bound
  device kind) for decapsulation to happen.
- Build environment: clang-sys picks the newest libclang (19, under the
  multiarch directory) whose resource headers it cannot find, and bindgen
  then fails on `<stdatomic.h>`; `LIBCLANG_PATH=/usr/lib/llvm-18/lib` is
  required here. Meson reserves option names starting with `rust_`.
  The 1.88 toolchain here has no clippy component, so clippy ran on stable.
  `cargo clippy --all-targets` builds the umbrella as a test despite
  `test = false`, so it keeps `no_std` and the panic handler out of test
  builds.
- The archive is always built with the release profile and the default
  cargo toolchain, whatever the meson buildtype; manifests are listed as
  extra meson dependencies because cargo dep-info omits them, but a
  toolchain change does not trigger a rebuild. `LIBCLANG_PATH` is read from
  the build environment, not captured at configure time.
- `-Ddataplane_rust=auto` turns Rust on wherever cargo exists, which then
  also requires libclang and crates.io access; before CI or packaging, the
  default should be `disabled` or the crates vendored.
- `compiler_builtins` objects in the archive reference
  `rust_eh_personality`. None is linked today; if C code ever resolved a
  libgcc helper from the Rust archive first, the link would fail.
- Device unit tests link natively only because unused sections, including
  the exported constructor and its shim references, are garbage-collected;
  sys tests define shim stand-ins instead.

## What production needs

- Go control plane: a `devices/vxlan/controlplane` package with protos,
  service and CGO calls into the C api (or a Rust api crate exporting C
  functions through the umbrella), and validation of addresses and VNI.
- CLI and web: a `yanet-cli-device-vxlan` crate registered in the root
  `Cargo.toml`, the `Makefile` and `debian/yanet2-cli.install`; a web page.
- Plugin path: builtin Rust items need a dataplane rebuild. Out-of-tree Rust
  devices need plugin device loading (`dp_load_device` only searches the main
  binary today) and one Rust runtime per plugin `.so`.
- ABI versioning: the Rust body and the opaque header sizes come from the
  headers at build time; a `YANET_MODULE_ABI_VERSION` check and a body
  version or magic checked on commit are needed before Rust items can ship
  separately from the C core.
- CI: cargo, a libclang with resource headers and `LIBCLANG_PATH` on the
  dataplane build image; the dataplane workspace added to fmt, clippy, test
  and Miri jobs.
- Performance: an A/B against a C implementation, and inlining or porting
  the shim helpers if the out-of-line calls show up.
