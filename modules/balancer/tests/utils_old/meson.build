dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_balancer_cp_dep,
  lib_balancer_dp_dep,
  libdpdk_dep,
]

includes = include_directories('../../')

sources = files(
  'mock.c',
  'process_packets.c',
  'packet.c'
)

lib_balancer_test_utils = static_library(
  'balancer_test_utils',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer_test_utils_dep = declare_dependency(
  link_with: lib_balancer_test_utils,
)
