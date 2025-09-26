cp_dependencies = [
  lib_common_dep,
  lib_filter_dep,
  lib_packet_dp_dep,
  lib_config_cp_dep,
  lib_counters_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_acl_cp = static_library(
  'acl_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_acl_cp_dep = declare_dependency(
  link_with: lib_acl_cp,
)
