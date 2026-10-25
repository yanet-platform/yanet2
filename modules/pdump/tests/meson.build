# Pdump capture unit tests.

pdump_test_fixture_sources = files(
  'fixture.c',
)

pdump_capture_test_sources = pdump_test_fixture_sources + files(
  'capture_test.c',
)

pdump_capture_test_exe = executable(
  'pdump_capture_test',
  pdump_capture_test_sources,
  c_args: ['-UNDEBUG'],
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_ring_objects_dep,
    lib_pdump_dp_dep,
    lib_agent_cp_dep,
    lib_counters_dep,
    libdpdk_dep,
    libpcap_dep,
  ],
  include_directories: [yanet_rootdir, pdump_dataplane_inc],
)

test(
  'pdump',
  pdump_capture_test_exe,
  timeout: 30,
)

pdump_bench_sources = pdump_test_fixture_sources + files(
  'pdump_bench.c',
)

pdump_bench_exe = executable(
  'pdump_bench',
  pdump_bench_sources,
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_ring_objects_dep,
    lib_pdump_dp_dep,
    lib_agent_cp_dep,
    lib_counters_dep,
    libdpdk_dep,
    libpcap_dep,
  ],
  include_directories: [yanet_rootdir, pdump_dataplane_inc],
)

benchmark('pdump_bench', pdump_bench_exe, timeout: 300)

limit_test_exe = executable(
  'pdump_limit_test',
  sources: files('limit_test.c'),
  c_args: yanet_test_c_args + ['-UNDEBUG'],
  include_directories: yanet_rootdir,
)

test('pdump_limit', limit_test_exe, timeout: 30)
