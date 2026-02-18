dependencies = [
  lib_common_dep,
  lib_agent_cp_dep,
  lib_balancer_state_dep,
  lib_filter_compiler_dep,
]

includes = include_directories('.', '../')

sources = files(
    'handler.c',
    'real.c',
    'selector.c',
    'update.c',
    'vs.c',
    'stats.c',
    'info.c'
)

lib_balancer_packet_handler = static_library(
  'balancer_packet_handler',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer_packet_handler_dep = declare_dependency(
  link_with: [lib_balancer_packet_handler],
)
