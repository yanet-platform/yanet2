dependencies = [
  libdpdk_dep,
  lib_common_dep,
  lib_errors_dep,
  lib_filter_compiler_dep,
  lib_filter_query_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
  lib_worker_dp_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_agent_cp_dep,
  lib_lib_utils_dep,
  lib_logging_dep,
]

sources = files(
  '../../api/controlplane.c',
  '../../dataplane/dataplane.c',
  'unrdup_test.c',
)

unrdup_test = executable(
  'unrdup_test',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir, '../../dataplane'],
  dependencies: dependencies,
)

test('unrdup', unrdup_test, suite: 'unrdup', env: [
  'UBSAN_OPTIONS=halt_on_error=0:print_stacktrace=0',
  'LSAN_OPTIONS=detect_leaks=0',
])
