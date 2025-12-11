dependencies = [
  lib_common_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_diag_dep,
]

sources = files(
  'agent.c',
)

lib_agent_cp = static_library(
  'agent',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_agent_cp_dep = declare_dependency(
  link_with: lib_agent_cp,
  include_directories: yanet_libdir,
)
