cp_dependencies = [
  lib_common_dep,
  lib_filter_compiler_dep,
  lib_balancer_state_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'module.c',
  'vs.c',
  'state.c',
  'info.c',
  'stats.c',
)

lib_balancer_cp = static_library(
  'balancer_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer_cp_dep = declare_dependency(
  link_with: [lib_balancer_cp, lib_filter_compiler, lib_balancer_state],
  dependencies: [lib_filter_compiler_dep, lib_balancer_state_dep],
)
