# vxlan: builtin Rust device with a Rust control-plane api (proof of concept)

A VXLAN device type whose dataplane, control-plane api and body layout are
written in Rust, wired into YANET the way the C devices are:

- the dataplane statically links one Rust archive and resolves
  `new_device_vxlan` from its own binary, like `plain`, `vlan` and `trafgen`;
- the Go control plane creates devices through a Rust api behind a
  cbindgen-generated C ABI, linked as the one Rust archive of the Go binary;
- a gRPC service, a CLI and lab scenario make it usable on a stand.

IPv4 underlay only. The web UI is not covered.

## Layout

```
lib/rust/                         dataplane workspace (no_std, panic = abort)
  yanet-shm/                      ShmLayout trait, fingerprint, validator,
                                  RelPtr/RelSlice/Opaque/Root, ShmItem
  yanet-shm-derive/               the audited #[derive(ShmLayout)]
  yanet-dp-sys/                   bindgen bindings, Device trait, Packet,
    shim/yanet_dp_shim.{c,h}      out-of-line packet-front and mbuf helpers
  yanet-dp-builtins/              umbrella staticlib linked into yanet-dataplane
  meson.build, cargo-staticlib.sh cargo -> meson custom_target with depfile
  cp/                             control-plane workspace (std, unwinds)
    yanet-cp-sys/                 agent memory, device blocks, C-ABI helpers,
      shim/yanet_cp_shim.{c,h}    device create/free/read over the agent library
    yanet-cp/                     umbrella staticlib + rure, the Go link's only
                                  Rust archive; build.rs writes the cbindgen
                                  header, meson copies it to build/lib/rust/cp/yanet_cp.h
devices/vxlan/
  config/                         VxlanConfig body, shared by both sides
  dataplane/                      device crate, #![no_std] #![forbid(unsafe_code)]
  api/                            Rust api crate, #![forbid(unsafe_code)]
  controlplane/                   Go service, ffi.go over yanet_cp.h, vxlanpb proto
  cli/                            yanet-cli-device-vxlan
  tests/device_test.c             dataplane_ut test: C ABI api + real loader
lab/scenarios/vxlan/              QEMU lab scenario with pcaps
```

C extension: `struct device` and `struct dp_device` carry `config_layout`, and
`cp_device_init_layout` refuses a device whose type was loaded for another
layout (`YANET_MODULE_ABI_VERSION` 33 -> 34).

## Architecture

### Shared-memory layouts (`yanet-shm`)

A type may live in shared memory only if it implements the unsafe trait
`ShmLayout`, which outside `yanet-shm` only `#[derive(ShmLayout)]` provides.
The derive accepts non-generic `#[repr(C)]` structs whose fields are all
`ShmLayout` (integers, arrays, `RelPtr<T>`, `RelSlice<T>`, `Opaque<T>`); a
`bool`, a reference, an absolute pointer or a struct without `repr(C)` does
not compile, and a hand-written `unsafe impl` is rejected by
`#![forbid(unsafe_code)]` (compile_fail doctests). The derive emits:

- `FINGERPRINT`: a deterministic fold of size, alignment and, per field, offset
  and field fingerprint. Swapping or widening a field changes it.
- `validate(validator, addr)`: checks the value lies in one readable region,
  aligned, and follows every relative pointer to a valid target.

`Opaque<T>` (`UnsafeCell<MaybeUninit<T>>`) holds bytes C mutates after
publish, so a reference to a struct containing them stays sound. A
`RelPtr` has no constructor and is neither `Clone` nor `Copy`, and the copy
helpers refuse types holding one, so a relative pointer exists only inside a
validated item in shared memory.
`RelPtr::get(root)` resolves with strict provenance: the slot gives only its
address, the target is `root.with_addr(slot + offset)`, where `Root` wraps the
raw pointer C handed over. `ShmItem<H, B> = { header: Opaque<H>, body: B }` is
a C header followed by a Rust body.

### Dataplane

- `yanet-dp-sys` runs bindgen over repository headers (device ABI,
  `device_ectx`, `packet`, opaque `cp_device`). It knows no device body.
- A device implements `Device { const NAME; type Config: ShmLayout + Sync;
  fn input; fn output }` in safe code and exports itself with
  `export_device!(vxlan, Vxlan)`. The macro accepts only an identifier and a
  path, fails the build unless the name equals `Vxlan::NAME` and is not a C
  device name, and emits `#[unsafe(export_name = "new_device_vxlan")]`.
- `new_device::<D>()` fills `struct device` with monomorphized handlers and
  `config_layout = D::Config::FINGERPRINT`, allocated with C `malloc`
  because `dp_load_device` frees it.
- A handler resolves `device_ectx->cp_device` (a relative slot) with strict
  provenance and reads it as `&ShmItem<cp_device, D::Config>`; no runtime
  check sits on the packet path. It then pops each input packet, passes it
  as `Packet` (first segment as `&mut [u8]`, length, hash, prepend, trim,
  reparse through the shim) and links it to output or drop by the verdict.
- `yanet-dp-builtins` is the only Rust archive in `yanet-dataplane`
  (`#![no_std]`, panic handler calls C `abort`, fat LTO).

### Layout binding

The dataplane reads a `cp_device` as `ShmItem<cp_device, B>` only in the
handlers of its own device type, and every `cp_device` of that type passed
`cp_device_init_layout` with `config_layout == B::FINGERPRINT`: the loader
copied the fingerprint from the Rust descriptor into `dp_device`, the init
compares it with what the creating control plane presents. A C creator
(`cp_device_init`, layout 0) and a control plane built for another layout are
refused once, at creation, with a failed-precondition error; nothing is
stored per config and nothing is checked per generation or per packet. It is
the device-type analogue of kernel module versioning (vermagic/modversions):
a binding of code to layout checked when they meet. The control plane's read
of a live device (`show`) checks the same fingerprint before copying body
bytes. The binding covers the body: the size of the C `cp_device` header is
assumed equal in the dataplane and the control plane, as for C devices
(both come from the same headers of one build). It does not defend against
a writer that changes published bytes;
"frozen after publish" stays a convention. The export macro refuses a zero
fingerprint, the value C devices register; a C device descriptor must set
`config_layout = 0` explicitly, because the loader copies whatever it holds.

### Control plane: Rust -> C ABI -> Go

- `devices/vxlan/api` (forbid unsafe) creates a device: validates the request
  (unicast addresses, nonzero unicast MACs, VNI <= 2^24-1, name lengths),
  asks the C shim for a block of `item_layout::<VxlanConfig>` bytes
  initialized for `VxlanConfig::FINGERPRINT`, writes the body, then validates
  the whole device before returning it: the block, the owner edge (must be
  this agent), the input/output pipeline edges and the body lie in the owner
  agent's arenas, aligned, the body is in range and reads back equal. A device
  that fails validation is destroyed again.
- `yanet-cp-sys` holds the unsafe parts: the agent's arena table walked
  through relative slots, device blocks, the shim calls, and the ABI
  helpers. The C shim is a meson library linked next to the archive
  (`-lyanet_cp_shim`), so it is compiled with the project's flags
  (sanitizers, cache-line size) and its header dependencies are tracked. Its safe API cannot reach memory outside the agent's: the agent
  carries the C layout only the shim produces; a validator exists only inside
  `Agent::validate` over the agent's own arenas; `create_device::<B>` and
  `read_live::<B>` take no offsets or sizes but derive the block size, the
  body offset, the read length and the presented fingerprint from `B`
  (non-zero, checked by the C side against the device type); a device block
  borrows its agent, reads and writes values only inside its body, never the
  C header before it, resolves header slots only to addresses, and refuses
  copies of values holding relative pointers. A `fixture` feature provides a heap-backed fake agent,
  borrowed by every handle it gives out and carrying its own layout, so api
  tests run under Miri without unsafe code.
- `yanet-cp` exports `yanet_cp_abi_version`, `yanet_cp_vxlan_device_new`,
  `..._free` and `..._show` with `#[repr(C)]` types only. Its build script
  runs cbindgen into the cargo output directory, and the meson target copies
  `yanet_cp.h` next to `libyanet_cp.a` in `build/lib/rust/cp`, where cgo and
  the C test include it; nothing generated lives in the source tree.
- Go: `devices/vxlan/controlplane/ffi.go` is the only file with cgo/`unsafe`;
  `NewDeviceVxlanDevice` checks the ABI version first; the service maps
  status codes to gRPC codes (invalid argument, not found, failed
  precondition, internal) and publishes through `agent.UpdateDevices`.

The safety contract at the boundary:

- every export runs its body under `catch_unwind` and returns a status code;
  a panic becomes `YANET_CP_PANIC` with a message, never an unwind into C or
  Go;
- results go only into caller buffers (error message of
  `YANET_CP_ERROR_LEN`, the tunnel, the device slot) or into the agent's
  shared memory; Rust keeps no state between calls (no globals, no threads,
  no signal handlers; std's panic machinery uses its own thread-local count,
  and the default panic hook prints to stderr);
- Go never holds a Rust-owned pointer: the device pointer is a block of the
  agent's shared memory, freed through `yanet_cp_vxlan_device_free`;
- arguments are `#[repr(C)]` values, zero-terminated strings and
  `(pointer, length)` arrays, read once and copied; null is checked.

Two Rust staticlibs in one link collide (`rust_eh_personality`, `__rdl_*`).
Go used to link `librure.a` (Rust); the CP umbrella now depends on the `rure`
crate, re-exports its C API, and replaces `-lrure` in `controlplane/ffi` and
`bindings/go/dataplane_ut`, so every Go binary carries one Rust runtime. C
targets keep `librure.a`. The fat-LTO dataplane archive defines no runtime
symbol, so it links next to the CP archive (C test, tagged Go harness).

## Device behaviour

- Output (RFC 7348 section 5): drops frames shorter than an Ethernet header;
  prepends outer Ethernet (local -> remote MAC), IPv4 (local -> remote, DF,
  ID 0, TTL 64, header checksum), UDP (destination 4789, checksum zero,
  source port `49152 | ((hash ^ hash >> 16) & 0x3fff)`) and VXLAN (flags 0x08,
  reserved bits zero, VNI); re-parses so later stages see the outer offsets.
  `packet->hash` is the parser's CRC-32C over the inner addresses and L4
  ports, the inner-flow hash the RFC recommends, mapped into the RFC 6335
  range; a frame the parser hashes nothing for (ARP) uses port 49152.
- Input: only an unfragmented IPv4 UDP datagram to the local address and port
  4789 is tunnel traffic. With the I flag and the configured VNI the outer
  stack (IPv4 options honoured) is stripped and the inner frame re-parsed; a
  zero UDP checksum is accepted and reserved bits ignored, as the RFC
  requires, and a non-zero checksum is not verified. Other tunnel traffic to
  the local address is dropped; everything else passes.
- Limits: classification reads the first segment only (a split header drops
  or passes undecapsulated); outer IPv4/UDP lengths are not checked against
  the frame; outer fragments pass.
- A vxlan device is a logical device: the initial generation gives every port
  a `plain` device, so traffic reaches vxlan only through forward rules (see
  the stand recipe).

## Build and test

```
git submodule update --init
meson setup build -Ddataplane_rust=enabled   # auto: on when cargo is found
meson compile -C build
meson test -C build vxlan_device
nm -D build/dataplane/yanet-dataplane | grep new_device_vxlan
cargo build --release --workspace            # CLIs, incl. yanet-cli-device-vxlan

cd lib/rust && cargo +1.88 test --workspace
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test --workspace
MIRIFLAGS="-Zmiri-strict-provenance -Zmiri-tree-borrows" cargo +nightly miri test --workspace
cd cp && cargo +1.88 test --workspace
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-cp-sys -p yanet-vxlan-api

go test ./devices/vxlan/...                                      # any build
go test -tags yanet_dataplane_rust ./devices/vxlan/...           # needs the Rust dataplane
```

bindgen needs libclang; `CLANG_PATH` and `LIBCLANG_PATH`, when set, must name
the same LLVM major version (clang's include paths with another libclang
break `<stdatomic.h>`). The control-plane archive needs only cargo and a C
compiler. `.github/workflows/rust-device-miri.yml` runs both workspaces'
tests and the Miri matrix (Stacked and Tree Borrows, strict provenance).

## Stand recipe

1. Build as above with `-Ddataplane_rust=enabled`; install the dataplane,
   control plane and CLIs as usual. The dataplane log shows
   `load device vxlan`.
2. Enable the service in the control-plane config (the shipped
   `controlplane.d/default.yaml` already has it):
   ```yaml
   devices:
     vxlan:
       instance_id: 0
       memory_requirements: 16MB
   ```
3. Steering. The port device stays `plain`; forward rules hand traffic to
   the logical vxlan device, and the vxlan pipelines hand results back to the
   port. With `PORT` the underlay port, local endpoint 192.0.2.1 and an
   overlay prefix 203.0.113.0/24 (see `lab/scenarios/vxlan/*.yaml`):
   ```
   yanet-cli-forward update --name=vx_to_port vx_to_port.yaml   # OUT -> PORT
   yanet-cli-function update --name=fn:vx_inner --chains out:1=forward:vx_to_port
   yanet-cli-function update --name=fn:vx_outer --chains out:1=forward:vx_to_port
   yanet-cli-pipeline update --name=vx_inner --functions fn:vx_inner
   yanet-cli-pipeline update --name=vx_outer --functions fn:vx_outer
   yanet-cli-device-vxlan update -n vx0 -i vx_inner:1 -o vx_outer:1 \
     --local-ip 192.0.2.1 --remote-ip 198.51.100.7 \
     --local-mac <port MAC> --remote-mac <next-hop MAC> --vni 4660
   yanet-cli-forward update --name=vx_steer vx_steer.yaml       # 192.0.2.1/32 IN vx0, 203.0.113.0/24 OUT vx0
   yanet-cli-function update --name=fn:vx_steer --chains steer:1=forward:vx_steer
   yanet-cli-pipeline update --name=vx_port --functions fn:vx_steer
   yanet-cli-device-plain update --name=PORT --input vx_port:1 --output <port output pipeline>:1
   ```
   A device cannot bind one pipeline to both its input and output (the
   counter storage of the pipeline would be registered twice), hence two
   pipelines per direction.
4. Verify: `yanet-cli-device-vxlan show vx0`; per-device counters with
   `yanet-cli-counters -d vx0` (input rx/tx/drop grow with decapsulated and
   dropped tunnel packets, output with encapsulated ones); capture on the
   peer or with the pdump module: an overlay packet leaves as
   Ethernet/IPv4/UDP 4789/VXLAN VNI 4660, a tunnel packet to 192.0.2.1 with
   VNI 4660 leaves as its inner frame, another VNI is dropped.

## Lab (QEMU, real yanet-dataplane)

`lab/scenarios/vxlan/manifest.yaml` boots the dataplane with the default
config and a control plane trimmed to route, forward, plain and vxlan (the
full baseline set of agents exhausts the default control-plane memory),
runs the stand recipe on port `01:00.0`, isolates `virtio_user_kni0` (the
guest kernel's router solicitations otherwise land in the captures), and
probes. Expected pcaps come from `lab/scenarios/fixtures/generate.go`, which
computes the outer source port from the same CRC-32C flow hash.

```
just lab doctor
flock -o /tmp/yanet2-lab.lock just lab up
flock -o /tmp/yanet2-lab.lock just lab manifest validate lab/scenarios/vxlan/manifest.yaml
flock -o /tmp/yanet2-lab.lock just lab scenario run vxlan
flock -o /tmp/yanet2-lab.lock just lab down
```

Run on this branch (abridged):

```
PASS boot     custom-config
PASS step     create-vxlan
[✓] Updated device 'vx0'.
PASS step     show-vxlan
Local IP:   192.0.2.1
Remote IP:  198.51.100.7
Local MAC:  52:54:00:6b:ff:a5
Remote MAC: 52:54:00:6b:ff:a1
VNI:        4660
DIRECTION │ PIPELINE │ WEIGHT
input     │ vx_inner │ 1
output    │ vx_outer │ 1
PASS step     bind-port
PASS probe    encap
PASS probe    decap
PASS probe    foreign-vni
```

The encapsulated frame matched byte for byte, including the derived source
port (0xfb27). The built-in `forward-route`, `decap` and `nat64` scenarios
pass on the same build.

## What is proven

- `nm -D build/dataplane/yanet-dataplane` lists `T new_device_vxlan` and no
  other Rust symbol; the binary has no `rust_eh_personality`/`__rdl_*`, only
  six local (`t`) Rust symbols: the generic handlers, the packet loop, the
  commit hook and the device's two handlers. The dataplane LTO object (1667 bytes of
  text) imports only `malloc` and the eight shim functions.
- `libyanet_cp.a` exports `yanet_cp_*` and all `rure_*` symbols, with the
  shim linked next to it from `libyanet_cp_shim.a`; Go builds and every Go
  package test passes with it in place of `librure.a`.
- `vxlan_device_test` (7 scenarios, dataplane_ut): the loader registers vxlan
  with all handlers; a device created through the C ABI of the Rust api
  decapsulates and re-encapsulates a tunnel packet, drops another VNI,
  encapsulates a plain frame; the api refuses a 25-bit VNI and shows the
  published tunnel; a C init of a vxlan device and a read for another layout
  are refused.
  Mutations (decap returning pass; the C init layout check disabled; the
  read layout check disabled) fail it.
- Rust tests: device 24 (wire format, RFC 7348 receive rules, edge cases),
  dp-sys 6 (+1 ignored negative), shm 14, cp-sys 10, api 14;
  compile_fail doctests: derive on bool/&T/*const T/non-repr(C), manual impl
  under forbid, export under another name or a C device name. cp-sys tests
  pin that a block refuses header writes and that a block handed back by Go
  gives no body access.
- Miri, strict provenance, Stacked and Tree Borrows: all of the above Rust
  tests of shm, dp-sys, device, cp-sys and api pass; the ignored narrow-root
  test is reported as UB by Stacked Borrows only.
- Go: vxlanpb validation, service tests (unknown name, invalid requests,
  dataplane without vxlan -> FailedPrecondition, ABI check) in every build;
  with the tag: create/show, no arena leak over 32 updates, unknown pipeline,
  other device types, concurrent update and show.
- QEMU lab: encap, decap and foreign-VNI drop on the real dataplane, three
  consecutive runs.

## Numbers

### Generated vs hand-written

Lines (`wc -l`) in this branch. Generated code is produced at build time into
build output directories and is not committed; the vxlan `*.pb.go` follow
the repository rule for protos (generated by protoc next to the proto,
gitignored, as for vlan).

| Group | Hand-written | Generated at build time |
|---|---|---|
| yanet-shm + derive | 478 + 127, tests 261 | derive output per type (compiler only) |
| yanet-dp-sys (sys, build.rs, wrapper.h) + shim | 470 + 112, tests 272 | bindings.rs 813 (42 structs, 10 fns) |
| yanet-dp-builtins | 19 | – |
| yanet-cp-sys + shim | 598 + 277, fixture 146, tests 210 | – |
| yanet-cp (exports, build.rs, cbindgen.toml) | 285 | yanet_cp.h 107 |
| vxlan config / device / api | 36 / 272 / 268, tests 245 / 142 | – |
| CLI | 325 | tonic/prost client 446 |
| Go service, ffi, validation | 673, tests 848 | vxlan.pb.go 398 + vxlan_grpc.pb.go 163 |
| Proto | 55 | – |
| C extension (device ABI, init, C devices, counters) | 63 added | – |
| C test | 626 | – |
| Meson, wrapper script | 187 | – |
| Lab scenario (yaml) + fixture generator | 136 + 74 | 5 pcaps, committed as fixtures like the other scenarios' |

`unsafe` (lines mentioning it): yanet-shm 17, dp-sys 31, cp-sys 33 + fixture
5, cp umbrella 12; config, device and api crates: one each, the forbid
attribute.

Build (this host): clean release of the DP umbrella 9.6 s, of the CP umbrella
(with regex and the cbindgen build step) 9.8 s; DP archive 4.2 MB, CP
archive 26 MB, both mostly `compiler_builtins`/std the link does not pull;
meson no-op does not run cargo.

## Friction and where the design did not hold

- Layout binding moved three times: a C-declared body behind a sealed per-type
  trait (sys knew every body), a per-config SDK header checked on the packet
  path, and now a fingerprint in the device type checked once at creation,
  which needed a small generic C extension and an ABI bump.
- The fingerprint is structural: two bodies with the same shape but different
  meaning share it, and a recursive body type does not compile
  (`RelPtr<Self>` would make the fingerprint const cyclic).
- `forbid(unsafe_code)` does not see unsafe tokens produced by a macro or
  derive from another crate; that is what lets the derive and the export macro
  work, and also why their output must be audited.
- The control-plane validation checks the edges it knows (owner, input and
  output entries) and the body; it does not walk the C-owned interior of the
  header (memory context, counter registry).
- `C_DEVICE_NAMES` in the dataplane sys crate mirrors the builtin C device
  list of `dataplane.c` by hand; nothing checks the two stay equal.
- A failed destroy of a never-published device (a C refusal during cleanup)
  leaves its block to the agent's wholesale reclaim.
- `dp_load_device` frees the descriptor, so Rust must allocate with C malloc.
- Packet-front and mbuf helpers are inline C/DPDK, so each is an out-of-line
  shim call per packet; decap and encap re-run the C parser. Throughput is
  not measured.
- A device cannot bind one pipeline to both entries, and the full functional
  control-plane config exhausts the default control-plane memory once a
  vxlan agent is added; the lab scenario works around both.
- The shared `rure` path dependency is not lint-capped by cargo, so the
  control-plane archive build passes `--cap-lints allow` (as `librure` does).
- The cbindgen header is found through cargo's JSON messages (the build
  script's output directory) by the meson wrapper; cargo has no stable way
  to place a build-script artifact.
- `cargo clippy --all-targets` builds the dataplane umbrella as a test
  despite `test = false`, so `no_std` and the panic handler are compiled out
  of test builds.
- Go caches link results without hashing external C archives; after the
  shared-memory change the control-plane binary had to be relinked with a
  clean Go cache (AGENTS.md documents this) — a stale binary showed up as
  "device type not found" in the lab.

## What production needs

- Out-of-tree Rust devices: plugin device loading (`dp_load_device` searches
  only the main binary) and one Rust runtime per plugin.
- A layout identity beyond structure (versioned type names) if two bodies may
  share a shape.
- Web UI page; CI images with libclang for the dataplane archive; vendored
  crates for offline packaging; `dataplane_rust` default decided per target.
- Performance: an A/B against a C implementation and inlining or porting the
  shim helpers if the calls show up.
