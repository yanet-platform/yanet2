dependencies = []

dependencies += lib_common_dep
dependencies += libdpdk_inc_dep

sources = files(
  'packet.c',
  'encap.c',
  'decap.c',
)

lib_packet_dp = static_library(
  'packet',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_packet_dp_dep = declare_dependency(
  link_with: lib_packet_dp,
  dependencies: dependencies,
  include_directories: yanet_libdir,
)

