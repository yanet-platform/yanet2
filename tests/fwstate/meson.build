fwmap_test_deps = [lib_common_dep, lib_fwstate_registry_dep, lib_statemap_dep]

fwmap_test_basic = executable(
  'fwmap_basic_test',
  ['fwmap_basic_test.c'],
  c_args: yanet_test_c_args,
  dependencies: fwmap_test_deps,
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
  dependencies: fwmap_test_deps,
)
benchmark('fwmap_bench-st', fwmap_bench_test, suite: ['fwstate'], timeout: 300)

fwmap_heavy_bench_test = executable(
  'fwmap_heavy_bench_test',
  ['fwmap_heavy_bench_test.c'],
  dependencies: fwmap_test_deps,
)
# Needs a 1 GiB arena on a hugetlbfs mount at /dev/hugepages.
benchmark('fwmap_heavy_bench-mt', fwmap_heavy_bench_test, suite: ['fwstate'], timeout: 300)

fwtable_test = executable(
  'fwtable_test',
  ['fwtable_test.c'],
  c_args: yanet_test_c_args,
  dependencies: fwmap_test_deps,
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
  dependencies: fwmap_test_deps,
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
  dependencies: [libdpdk_test_dep, lib_common_dep, lib_fwstate_dep],
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
  dependencies: [lib_common_dep, lib_fwstate_dep, lib_packet_dp_dep, lib_lib_utils_dep, lib_statemap_dep],
)
test('check_state_fragment',
  check_state_fragment_test,
  timeout: 15,
  suite: ['fwstate'],
)
