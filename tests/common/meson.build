dependencies = [lib_common_dep, lib_logging_dep]

lpm_test = executable(
  'lpm_test',
  ['lpm_test.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('lpm', lpm_test, suite: ['common'])

radix_test = executable(
  'radix_test',
  ['radix_test.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('radix', radix_test, suite: ['common'])

table_test = executable(
  'table_test',
  ['value_table.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)
test('value_table', table_test, suite: ['common'])

ttlmap_test = executable(
  'ttlmap_test',
  ['ttlmap_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies + [libdpdk_dep, lib_logging_dep],
  include_directories: yanet_rootdir
)
test('ttlmap', ttlmap_test, suite: ['common'])

rcu_test = executable(
  'rcu_test',
  ['rcu_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)
test('rcu', rcu_test, suite: ['common'])

executable(
  'rcu_bench',
  ['rcu_bench.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)

balloc_test = executable(
  'balloc_test',
  ['balloc_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)
test('balloc', balloc_test, suite: ['common'])

executable(
  'balloc_bench',
  ['balloc_bench.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)

big_array_test = executable(
  'big_array_test',
  ['big_array_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)
test('big_array', big_array_test, suite: ['common'])

btree_test = executable(
  'btree_test',
  ['btree_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)
test('btree', btree_test, suite: ['common'])

executable(
  'btree_bench_u16',
  ['btree_bench_u16.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)

executable(
  'btree_bench_u32',
  ['btree_bench_u32.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)

executable(
  'btree_bench_u64',
  ['btree_bench_u64.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies
)

thread_dep = dependency('threads')

data_pipe_test = executable(
  'data_pipe_test',
  ['data_pipe_tsan.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, thread_dep],
  include_directories: yanet_rootdir,
)
test('data_pipe', data_pipe_test, suite: ['common'])
