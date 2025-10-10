includes = [
  yanet_rootdir,
  '../..', 
]

dependencies = [
  lib_common_dep, lib_packet_dp_dep, lib_balancer_cp_dep, lib_balancer_dp_dep, libdpdk_inc_dep
]

sources = files(
  'balancer.c', 'packet.c'
)

lib_balancer_test_utils = static_library(
  'balancer_test_utils',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: includes,
  dependencies: dependencies,
  install: false,
)

lib_balancer_test_utils_dep = declare_dependency(
  link_with: lib_balancer_test_utils,
  include_directories: includes + ['.'],
  dependencies: [lib_packet_dp_dep, lib_balancer_cp_dep, lib_balancer_dp_dep, lib_config_dp_dep, lib_config_cp_dep]
)