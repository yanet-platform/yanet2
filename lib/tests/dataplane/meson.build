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

parse_frag_test = executable(
  'parse_frag_test',
  'parse_frag_test.c',
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
test('parse_frag', parse_frag_test, suite: 'lib_packet')

encap_dscp_test = executable(
  'encap_dscp_test',
  'encap_dscp_test.c',
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
test('encap_dscp', encap_dscp_test, suite: 'lib_packet')

clock_test = executable(
  'clock_test',
  'clock_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('clock', clock_test, suite: 'lib_dataplane')

topology_test = executable(
  'topology_test',
  'topology_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_config_dp_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('topology', topology_test, suite: 'lib_dataplane')

zone_test = executable(
  'zone_test',
  'zone_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_config_dp_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test('zone', zone_test, suite: 'lib_dataplane')

worker_clone_test = executable(
  'worker_clone_test',
  'worker_clone_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_worker_dp_dep,
    lib_dataplane_ut_dep,
    lib_logging_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)
test('worker_clone', worker_clone_test, suite: 'lib_worker')
