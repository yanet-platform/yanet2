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
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_ring_objects_dep,
    lib_pdump_dp_dep,
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
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_ring_objects_dep,
    lib_pdump_dp_dep,
    libdpdk_dep,
    libpcap_dep,
  ],
  include_directories: [yanet_rootdir, pdump_dataplane_inc],
)

benchmark('pdump_bench', pdump_bench_exe, timeout: 300)
