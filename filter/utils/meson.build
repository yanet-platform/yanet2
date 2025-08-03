dependencies = [
  lib_common_dep, lib_packet_dp_dep, lib_filter_dep,
]

sources = files(
  'utils.c',
)

lib_filter_utils = static_library(
  'filter_utils',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_filter_utils_dep = declare_dependency(
  link_with: lib_filter_utils,
  include_directories: include_directories('.'),
)