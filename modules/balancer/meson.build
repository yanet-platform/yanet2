# common dependecies
dependencies += lib_common_dep
dependencies += lib_filter_dep
dependencies += lib_logging_dep

# dataplane dependecies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep

dp_dependencies += lib_module_dp_dep

dp_sources = files(
  'dataplane.c',
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
  link_args: [
    '-Wl,--defsym', '-Wl,new_module_balancer=new_module_balancer',
    '-Wl,--export-dynamic-symbol=new_module_balancer',
  ],
)

# controlplane dependecies
cp_dependencies = dependencies

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

subdir('fuzzing')
