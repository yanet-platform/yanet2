dependencies = [
  lib_common_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

sources = files(
  'decap.c',
  'dscp.c',
  'encap.c',
  'packet.c',
  'mss.c',
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
  include_directories: yanet_rootdir,
)

