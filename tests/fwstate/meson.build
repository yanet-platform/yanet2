dependencies = [lib_common_dep, lib_logging_dep, lib_fwstate_dep]

fwmap_test_basic = executable(
  'fwmap_basic_test',
  ['fwmap_basic_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test(
  'fwmap_basic',
  fwmap_test_basic,
  timeout: 15,
  suite: ['fwstate'],
)

fwmap_bench_test = executable(
  'fwmap_bench_test',
  ['fwmap_bench_test.c'],
  c_args: yanet_c_args,  # not testing args
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('fwmap_bench-st', fwmap_bench_test, suite: ['fwstate', 'large'])

fwmap_heavy_bench_test = executable(
  'fwmap_heavy_bench_test',
  ['fwmap_heavy_bench_test.c'],
  c_args: yanet_c_args,  # not testing args
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('fwmap_heavy_bench-mt', fwmap_heavy_bench_test, suite: ['fwstate', 'large'])

fwtable_test = executable(
  'fwtable_test',
  ['fwtable_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('fwtable_locks',
  fwtable_test,
  timeout: 15,
  suite: ['fwstate'],
)

fwstate_cursor_test = executable(
  'fwstate_cursor_test',
  ['fwstate_cursor_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('fwstate_cursor',
  fwstate_cursor_test,
  timeout: 15,
  suite: ['fwstate'],
)

sync_test = executable(
  'sync_test',
  ['sync_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_logging_dep, lib_fwstate_dep, lib_packet_dp_dep],
)
test('sync',
  sync_test,
  timeout: 15,
  suite: ['fwstate'],
)

check_state_fragment_test = executable(
  'check_state_fragment_test',
  ['check_state_fragment_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_logging_dep, lib_fwstate_dep, lib_packet_dp_dep, lib_lib_utils_dep],
)
test('check_state_fragment',
  check_state_fragment_test,
  timeout: 15,
  suite: ['fwstate'],
)
