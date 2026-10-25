dp_dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_route_mpls_dp = static_library(
  'route_mpls_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_route_mpls_dp_dep = declare_dependency(
  link_with: lib_route_mpls_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_route_mpls',
    '-Wl,--export-dynamic-symbol=new_module_route_mpls',
  ],
)
