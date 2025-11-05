dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  libdpdk_dep,
]

includes = include_directories('../../')

sources = files(
  'yanet_mock.c',
)

lib_yanet_test_utils = static_library(
  'yanet_test_utils',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_yanet_test_utils_dep = declare_dependency(
  link_with: lib_yanet_test_utils,
)

test_mock = executable('mock_test', sources: ['mock_test.c'], dependencies: dependencies + [lib_yanet_test_utils_dep])
test('mock', test_mock)