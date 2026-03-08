dp_dependencies = [
  lib_common_dep,
  lib_filter_query_dep,
  lib_logging_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_route_mpls_dp = static_library(
  'route_mpls-dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_route_mpls_dp_dep = declare_dependency(
  link_with: lib_route_mpls_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_route_mpls=new_module_route_mpls',
    '-Wl,--export-dynamic-symbol=new_module_route_mpls',
  ],
)
