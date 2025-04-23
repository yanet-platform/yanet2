# dataplane dependencies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep
dp_dependencies += lib_module_dp_dep

dp_sources = files(
  'dataplane.c',
)

lib_route_dp = static_library(
  'route_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_route_dp_dep = declare_dependency(
  link_with: lib_route_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_route=new_module_route',
    '-Wl,--export-dynamic-symbol=new_module_route',
  ],
)
