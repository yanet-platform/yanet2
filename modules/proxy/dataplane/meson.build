cargo = find_program('cargo', required: true)

if get_option('buildtype') == 'release'
  cargo_profile = 'release'
  cargo_profile_dir = 'release'
else
  cargo_profile = 'dev'
  cargo_profile_dir = 'debug'
endif

dpdk_root = meson.project_source_root() / 'subprojects' / 'dpdk'

rust_lib_name = 'libyanet_proxy_dp.a'
rust_target_dir = meson.current_build_dir() / 'target'
rust_lib_path = rust_target_dir / cargo_profile_dir / rust_lib_name

custom_target(
  'proxy_dp_rust',
  output: rust_lib_name,
  command: [
    cargo, 'build',
    '--manifest-path', meson.current_source_dir() / 'Cargo.toml',
    '--target-dir', rust_target_dir,
    cargo_profile == 'release' ? '--release' : '',
  ],
  env: {
    'DPDK_ROOT': dpdk_root,
    'CARGO_MANIFEST_DIR': meson.current_source_dir(),
  },
)

dp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

lib_proxy_dp = static_library(
  'proxy_dp',
  objects: rust_lib_path,
  dependencies: dp_dependencies,
  install: false
)

lib_proxy_dp_dep = declare_dependency(
  link_with: lib_proxy_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_proxy=new_module_proxy',
    '-Wl,--export-dynamic-symbol=new_module_proxy',
  ]
)
