dependencies = [
  lib_common_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

dp_dependencies = dependencies + [
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'dataplane.c', 'state.c'
)

lib_balancer_dp = static_library(
  'balancer_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_balancer_dp_dep = declare_dependency(
  link_with: lib_balancer_dp,
  dependencies: [libdpdk_inc_dep]
)

cp_dependencies = dependencies + [lib_config_cp_dep]

sources = files(
  'controlplane.c',
)

lib_balancer_cp = static_library(
  'balancer_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  install: false,
)

lib_balancer_cp_dep = declare_dependency(
  link_with: lib_balancer_cp,
)

subdir('controlplane')
subdir('fuzzing')
subdir('tests')
