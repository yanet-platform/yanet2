dependencies = [
  lib_common_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_errors_dep,
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
  dependencies: [lib_errors_dep],
  include_directories: yanet_libdir,
)

test_agent_attach = executable(
  'agent_attach_test',
  'agent_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_agent_cp_dep, lib_config_dp_dep, lib_config_cp_dep, lib_logging_dep],
  include_directories: yanet_rootdir,
  link_language: 'c',
)
test('agent_attach', test_agent_attach, suite: ['lib'])
