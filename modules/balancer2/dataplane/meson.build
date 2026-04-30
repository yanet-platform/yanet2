dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
  lib_worker_dp_dep,
  lib_filter_query_dep,
]

includes = include_directories('.', '../../../')

dp_sources = files(
  'dataplane.c',
)

lib_balancer2_dp = static_library(
  'balancer2_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  include_directories: includes,
  install: false,
)

lib_balancer2_dp_dep = declare_dependency(
  link_with: lib_balancer2_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_balancer2=new_module_balancer2',
    '-Wl,--export-dynamic-symbol=new_module_balancer2',
  ],
)
