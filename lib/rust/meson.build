# Builtin Rust items: one cargo staticlib linked into yanet-dataplane.
#
# Disabled when cargo is missing under the default auto setting; the
# dataplane then loads no Rust device types.
dp_cargo = find_program('cargo', required: get_option('dataplane_rust'))
rust_builtins_enabled = dp_cargo.found()

if rust_builtins_enabled
  lib_rust_dp_shim = static_library(
    'yanet_dp_shim',
    files('yanet-dp-sys/shim/yanet_dp_shim.c'),
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: [lib_packet_dp_dep, lib_module_dp_dep],
    install: false,
  )

  lib_rust_dp_builtins = custom_target(
    'yanet_dp_builtins',
    output: 'libyanet_dp_builtins.a',
    depfile: 'libyanet_dp_builtins.d',
    # Cargo dep-info lists sources and bound headers, not manifests.
    depend_files: files(
      'Cargo.lock',
      'Cargo.toml',
      'yanet-dp-builtins/Cargo.toml',
      'yanet-dp-sys/Cargo.toml',
      '../../devices/vxlan/dataplane/Cargo.toml',
    ),
    command: [
      files('cargo-staticlib.sh'),
      dp_cargo,
      meson.current_source_dir() / 'Cargo.toml',
      meson.current_build_dir() / 'target',
      'yanet-dp-builtins',
      '@OUTPUT@',
      '@DEPFILE@',
    ],
    console: true,
  )

  lib_rust_dp_builtins_dep = declare_dependency(
    link_with: [lib_rust_dp_builtins, lib_rust_dp_shim],
  )
endif

# The control-plane archive carries the regex C API the counters need, so it
# is built whenever the control plane is.
if not dataplane_only
  subdir('cp')
endif
