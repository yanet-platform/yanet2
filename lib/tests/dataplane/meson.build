subdir('abi_plugin/good')
subdir('abi_plugin/bad')
subdir('abi_plugin/missing')
subdir('abi_plugin/unknown')
subdir('abi_plugin/no_count')
subdir('abi_plugin/zero_count')

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

plugin_loader_test = executable(
  'plugin_loader_test',
  'plugin_loader_test.c',
  c_args: yanet_c_args + [
    '-DABI_TEST_GOOD_PLUGIN_DIR="@0@"'.format(abi_test_good_plugin_dir),
    '-DABI_TEST_BAD_PLUGIN_DIR="@0@"'.format(abi_test_bad_plugin_dir),
    '-DABI_TEST_MISSING_PLUGIN_DIR="@0@"'.format(abi_test_missing_plugin_dir),
    '-DABI_TEST_UNKNOWN_PLUGIN_DIR="@0@"'.format(abi_test_unknown_plugin_dir),
    '-DABI_TEST_NO_COUNT_PLUGIN_DIR="@0@"'.format(abi_test_no_count_plugin_dir),
    '-DABI_TEST_ZERO_COUNT_PLUGIN_DIR="@0@"'.format(abi_test_zero_count_plugin_dir),
  ],
  link_args: yanet_link_args,
  # Resolves good.c's packet_decap; drop it and the good case misreports a
  # link gap as an ABI mismatch.
  link_whole: [lib_packet_dp],
  export_dynamic: true,
  dependencies: [
    lib_config_dp_dep,
    lib_logging_dep,
  ],
  include_directories: yanet_rootdir,
)
test(
  'plugin_loader',
  plugin_loader_test,
  suite: 'lib_dataplane',
  depends: [
    lib_test_plugin_good,
    lib_test_plugin_bad,
    lib_test_plugin_missing,
    lib_test_plugin_unknown,
    lib_test_plugin_no_count,
    lib_test_plugin_zero_count,
  ],
)
