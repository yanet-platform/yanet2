dependencies = []

dependencies += lib_common_dep
dependencies += lib_config_dp_dep

sources = files(
  'agent.c',
)

lib_agent_cp = static_library(
  'agent',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_agent_cp_dep = declare_dependency(
  link_with: lib_agent_cp,
  include_directories: yanet_libdir,
)
