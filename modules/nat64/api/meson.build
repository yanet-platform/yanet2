cp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_config_dp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'nat64cp.c',
)

lib_nat64_cp = static_library(
  'nat64_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_nat64_cp_dep = declare_dependency(
  link_with: lib_nat64_cp,
)
