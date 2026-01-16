subdir('balancerpb')

dependencies = [
  lib_common_dep,
  lib_agent_cp_dep,
  lib_balancer_cp_dep,
]

sources = files(
    'agent.c',
    'config.c',
    'manager.c'
)

static_library(
  'balancer_agent',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: [yanet_rootdir],
  install: false,
)