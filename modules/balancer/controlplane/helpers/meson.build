dependencies = [
  lib_common_dep,
  lib_agent_cp_dep,
  lib_filter_compiler_dep,
]

includes = include_directories('.', '../../../../')

sources = files(
  'balancer.c',
  'agent.c',
  'sessions.c',
  'vs.c',
  'real.c',
)

static_library(
  'balancer_helpers',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)
