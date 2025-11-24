dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_counters_dep,
  libdpdk_inc_dep,
]

includes = include_directories('../../')

sources = files(
  'mock.c',
  'worker.c',
  '../../dataplane/dataplane.c', # for symbols `dataplane_load_module` and `dataplane_load_device`
)

lib_yanet_mock = static_library(
  'yanet_mock',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_yanet_mock_dep = declare_dependency(
  link_with: lib_yanet_mock,
)