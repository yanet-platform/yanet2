# controlplane

cp_dependencies = [
  lib_common_dep,
  lib_filter_compiler_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
  libdpdk_inc_dep,
]

sources = files(
  'controlplane.c',
)

lib_my_module_cp = static_library(
  'my_module_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_my_module_cp_dep = declare_dependency(
  link_with: [lib_my_module_cp],
)

# dataplane

dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
  lib_filter_query_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_my_module_dp = static_library(
  'my_module_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_my_module_dp_dep = declare_dependency(
  link_with: lib_my_module_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_balancer=new_module_balancer',
    '-Wl,--export-dynamic-symbol=new_module_balancer'
  ],
)

