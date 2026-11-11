dependencies = [
  lib_common_dep,
  libdpdk_inc_dep,
]

# The parser logs only in a trace build.
if get_option('trace').enabled()
  dependencies += lib_logging_dep
endif

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

