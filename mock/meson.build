lib_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_counters_dep,
  lib_pipeline_dp_dep,
  libdpdk_dep,
]

sources = files(
  'mock.c',
  'worker.c',
)

lib_yanet_mock = static_library(
  'yanet_mock',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args + ['-Wl,-E',],
  dependencies: dependencies,
  include_directories: [yanet_rootdir],
  install: false,
)

lib_yanet_mock_dep = declare_dependency(
  link_with: [lib_yanet_mock],
)

subdir('tests')
subdir('go')