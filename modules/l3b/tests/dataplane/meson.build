l3b_test_deps = [
  lib_common_dep,
  lib_errors_dep,
  lib_agent_cp_dep,
  lib_config_cp_dep,
  lib_config_dp_dep,
  lib_counters_dep,
  lib_l3state_dep,
  lib_l3b_objects_dep,
  lib_lib_utils_dep,
  lib_logging_dep,
  lib_packet_dp_dep,
  lib_statemap_dep,
]

l3b_test = executable(
  'l3b_test',
  'l3b_test.c',
  include_directories: [yanet_rootdir, '../../dataplane'],
  dependencies: [libdpdk_test_dep] + l3b_test_deps,
)

test('l3b', l3b_test, suite: 'l3b')

l3b_guard_test = executable(
  'l3b_guard_test',
  'l3b_guard_test.c',
  include_directories: [yanet_rootdir, '../../dataplane'],
  dependencies: [
    libdpdk_inc_dep,
    lib_common_dep,
    lib_lib_utils_dep,
    lib_logging_dep,
    lib_packet_dp_dep,
  ],
)

test('l3b_guard', l3b_guard_test, suite: 'l3b')
