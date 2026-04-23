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
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_packet_dp_dep = declare_dependency(
  compile_args: yanet_c_args,
  link_args: yanet_link_args,
  link_with: lib_packet_dp,
  dependencies: dependencies,
  include_directories: yanet_libdir,
)

mss_test = executable(
  'mss_test',
  'tests/mss_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_packet_dp_dep, lib_logging_dep, libdpdk_dep],
  include_directories: yanet_rootdir,
)
test('mss', mss_test, suite: 'lib_packet')
