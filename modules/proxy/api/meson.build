cp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
]

if get_option('buildtype') == 'release'
  cargo_profile = 'release'
  cargo_profile_dir = 'release'
else
  cargo_profile = 'dev'
  cargo_profile_dir = 'debug'
endif

rust_lib_name = 'libyanet_proxy_cp.a'
rust_target_dir = meson.project_source_root() / 'target'
rust_lib_path = rust_target_dir / cargo_profile_dir / rust_lib_name

lib_proxy_cp = static_library(
  'proxy_cp',
  objects: rust_lib_path,
  dependencies: cp_dependencies,
  install: false
)

lib_proxy_cp_dep = declare_dependency(
  link_with: lib_proxy_cp
)
