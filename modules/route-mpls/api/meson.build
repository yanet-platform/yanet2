cp_dependencies = [
  lib_common_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_route_mpls_cp = static_library(
  'route_mpls_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_route_mpls_cp_dep = declare_dependency(
  link_with: lib_route_mpls_cp,
)
