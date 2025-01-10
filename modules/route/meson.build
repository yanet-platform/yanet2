# common dependecies
dependencies += lib_common_dep
#dependencies += lib_filter_dep

# dataplane dependecies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep

dp_dependencies += lib_module_dp_dep

dp_sources = files(
  'dataplane.c',
)

lib_route_dp = static_library(
  'route_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_route_dp_dep = declare_dependency(
  link_with: lib_route_dp,
  link_args: [
    '-Wl,--defsym', '-Wl,new_module_route=new_module_route',
    '-Wl,--export-dynamic-symbol=new_module_route',
  ],
)

# controlplane dependecies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep

cp_sources = files(
  'controlplane.c',
)

lib_route_cp = static_library(
  'route_cp',
  cp_sources,
  dependencies: cp_dependencies,
  install: false,
)

lib_route_cp_dep = declare_dependency(
  link_with: lib_route_cp,
)

