dependencies = [lib_common_dep, lib_logging_dep, lib_fwstate_dep]

fwmap_test_basic = executable(
  'fwmap_basic_test',
  ['fwmap_basic_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('fwmap_basic', fwmap_test_basic, suite: ['fwstate'])

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

ttlmap_heavy_bench_test = executable(
  'ttlmap_heavy_bench_test',
  ['ttlmap_heavy_bench_test.c'],
  c_args: yanet_c_args,  # not testing args
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('ttlmap_heavy_bench-mt', ttlmap_heavy_bench_test, suite: ['fwstate', 'large'])

layermap_test = executable(
  'layermap_test',
  ['layermap_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('layermap_basic', layermap_test, suite: ['fwstate'])
