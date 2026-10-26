nat64_unittest = executable(
  'nat64_unittest',
  files('commands.c', 'main.c', 'nat64_test.c'),
  c_args: yanet_test_c_args,
  link_args: [
    '-Wl,--wrap=malloc',
    '-Wl,--wrap=calloc',
    '-Wl,--wrap=strdup',
    '-Wl,--wrap=free',
  ],
  include_directories: [yanet_rootdir, '../..', '../../dataplane'],
  dependencies: [
    libdpdk_test_dep,
    lib_common_dep,
    lib_logging_dep,
    lib_nat64_cp_dep,
    lib_nat64_dp_dep,
    lib_packet_dp_dep,
  ],
)

test('nat64_unittest', nat64_unittest, env: [
  'YANET_TEST=nat64_autotest',
  'UBSAN_OPTIONS=halt_on_error=0:print_stacktrace=0',
  'LSAN_OPTIONS=detect_leaks=0',
  'DPDK_TEST_PARAMS=--no-huge --iova-mode=va',
])

test('nat64_ownership_unittest', nat64_unittest, env: [
  'YANET_TEST=nat64_ownership_autotest',
  'UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1',
  'LSAN_OPTIONS=detect_leaks=1',
  'DPDK_TEST_PARAMS=--no-huge --iova-mode=va --file-prefix=nat64_ownership',
])
