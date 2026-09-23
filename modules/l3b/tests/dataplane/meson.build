dependencies = [
  libdpdk_dep,
  lib_common_dep,
  lib_errors_dep,
  lib_agent_cp_dep,
  lib_config_cp_dep,
  lib_config_dp_dep,
  lib_counters_dep,
  lib_filter_compiler_dep,
  lib_filter_query_dep,
  lib_l3state_dep,
  lib_l3b_objects_dep,
  lib_lib_utils_dep,
  lib_logging_dep,
  lib_module_dp_dep,
  lib_packet_dp_dep,
  lib_worker_dp_dep,
]

l3b_test = executable(
  'l3b_test',
  'l3b_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir, '../../dataplane'],
  dependencies: dependencies,
)

test('l3b', l3b_test, suite: 'l3b')

l3b_guard_test = executable(
  'l3b_guard_test',
  'l3b_guard_test.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir, '../../dataplane'],
  dependencies: dependencies,
)

test('l3b_guard', l3b_guard_test, suite: 'l3b')
