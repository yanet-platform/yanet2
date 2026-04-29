mss_test = executable(
  'mss_test',
  'mss_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_packet_dp_dep,
    lib_lib_utils_dep,
    lib_logging_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)
test('mss', mss_test, suite: 'lib_packet')

gre_test = executable(
  'gre_test',
  'gre_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_packet_dp_dep,
    lib_lib_utils_dep,
    lib_logging_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)
test('gre', gre_test, suite: 'lib_packet')
