dependencies = [lib_common_dep, lib_logging_dep]
thread_dep = dependency('threads')

lpm_test = executable(
  'lpm_test',
  ['lpm_test.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('lpm', lpm_test, suite: ['common'])

lpm_wide_test = executable(
  'lpm_wide_test',
  ['lpm_wide_test.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('lpm_wide', lpm_wide_test, suite: ['common'])

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

memory_context_tree_test = executable(
  'memory_context_tree_test',
  ['memory_context_tree_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('memory_context_tree', memory_context_tree_test, suite: ['common'])

balloc_thread_stress_test = executable(
  'balloc_thread_stress_test',
  ['balloc_thread_stress_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies + [thread_dep],
)
test('balloc_thread_stress', balloc_thread_stress_test, suite: ['common'])

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

hash_index_test = executable(
  'hash_index_test',
  ['hash_index_test.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)
test('hash_index', hash_index_test, suite: ['common'])

data_pipe_test = executable(
  'data_pipe_test',
  ['data_pipe_tsan.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, thread_dep],
  include_directories: yanet_rootdir,
)
test('data_pipe', data_pipe_test, suite: ['common'])

flatmap_test = executable(
  'flatmap_test',
  ['flatmap_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_logging_dep, lib_errors_dep],
  include_directories: yanet_rootdir,
)
test('flatmap', flatmap_test, suite: ['common'])

thash_test = executable(
  'thash_test',
  ['thash_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: yanet_rootdir,
)
test('thash', thash_test, suite: ['common'])

crc32_test = executable(
  'crc32_test',
  ['crc32_test.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  include_directories: yanet_rootdir,
)
test('crc32', crc32_test, suite: ['common'])

