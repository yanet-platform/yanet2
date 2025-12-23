dependencies = [
  lib_common_dep,
  lib_agent_cp_dep,
]

includes = include_directories('.')

sources = files(
  'session_table.c',
  'state.c',
  'registry.c',
  'index.c',
  'array.c',
  'service.c',
)

lib_balancer_state = static_library(
  'balancer_state',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer_state_dep = declare_dependency(
  link_with: [lib_balancer_state],
)
