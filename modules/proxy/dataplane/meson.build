if get_option('buildtype') == 'release'
  cargo_profile = 'release'
  cargo_profile_dir = 'release'
else
  cargo_profile = 'dev'
  cargo_profile_dir = 'debug'
endif

rust_lib_name = 'libyanet_proxy_dp.a'
rust_target_dir = meson.project_source_root() / 'target'
rust_lib_path = rust_target_dir / cargo_profile_dir / rust_lib_name

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
