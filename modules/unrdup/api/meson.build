cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_filter_compiler_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_unrdup_cp = static_library(
  'unrdup_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_unrdup_cp_dep = declare_dependency(
  link_with: lib_unrdup_cp,
)
