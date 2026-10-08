# The Rust control plane: one cargo staticlib the Go control plane links,
# and the C header its build script generates with cbindgen, both in this
# build directory. Uses the cargo the counters library requires.
#
# The C shim the archive calls is a meson library, so it gets the project's
# C flags (sanitizers, cache-line size) and header dependencies.
lib_rust_cp_shim = static_library(
  'yanet_cp_shim',
  files('yanet-cp-sys/shim/yanet_cp_shim.c'),
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_config_cp_dep, lib_agent_cp_dep, lib_errors_dep],
  include_directories: yanet_rootdir,
  install: false,
)

lib_rust_cp = custom_target(
  'yanet_cp',
  output: ['libyanet_cp.a', 'yanet_cp.h'],
  depfile: 'libyanet_cp.d',
  # Cargo dep-info lists sources, not manifests.
  depend_files: files(
    'Cargo.lock',
    'Cargo.toml',
    'yanet-cp-sys/Cargo.toml',
    'yanet-cp/Cargo.toml',
    'yanet-cp/build.rs',
    'yanet-cp/cbindgen.toml',
    '../../../devices/vxlan/api/Cargo.toml',
    '../../../devices/vxlan/config/Cargo.toml',
    '../yanet-shm/Cargo.toml',
    '../yanet-shm-derive/Cargo.toml',
  ),
  command: [
    files('../cargo-staticlib.sh'),
    cargo,
    meson.current_source_dir() / 'Cargo.toml',
    meson.current_build_dir() / 'target',
    'yanet-cp',
    '@OUTPUT0@',
    '@DEPFILE@',
    'yanet_cp.h',
  ],
  # The vendored regex crate is a path dependency, so cargo does not cap its
  # lints; this build is not where they are acted on, as for librure.
  env: {'RUSTFLAGS': '--cap-lints allow'},
  console: true,
)

lib_rust_cp_dep = declare_dependency(
  link_with: [lib_rust_cp[0], lib_rust_cp_shim],
  sources: lib_rust_cp[1],
  include_directories: include_directories('.'),
)
