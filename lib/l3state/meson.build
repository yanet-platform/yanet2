# L3State library - l3b flow state (session pinning) on the statemap core
dependencies = [
    lib_common_dep,
    lib_packet_dp_dep,
    libdpdk_dep,
    lib_statemap_dep,
]

sources = files(
  'lookup.c',
)

lib_l3state = static_library(
  'l3state',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_l3state_dep = declare_dependency(
  link_with: lib_l3state,
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
