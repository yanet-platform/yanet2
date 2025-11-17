dependencies = [
  lib_common_dep, lib_packet_dp_dep, libdpdk_inc_dep
]

sources = files(
  'filter.c',
  'helper.c',
)

lib_filter = static_library(
  'filter',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_filter_dep = declare_dependency(
  link_with: lib_filter,
  include_directories: include_directories('.'),
)

subdir('utils')
subdir('tests')
subdir('bench')
