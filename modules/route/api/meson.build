# controlplane dependencies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_route_cp = static_library(
  'route_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_route_cp_dep = declare_dependency(
  link_with: lib_route_cp,
)
