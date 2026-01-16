dependencies = [
  lib_common_dep,
  lib_agent_cp_dep,
  lib_balancer_state_dep,
  lib_balancer_packet_handler_dep,
  lib_filter_compiler_dep,
]

includes = include_directories('.', '../')

sources = files(
    'balancer.c',
)

lib_balancer_cp = static_library(
  'balancer_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer_cp_dep = declare_dependency(
  link_with: [lib_balancer_cp],
)

