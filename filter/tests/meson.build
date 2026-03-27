test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_filter_compiler_dep,
  lib_filter_query_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

# test basic ports
test_filter_basic_ports = executable(
  'basic_ports',
  ['basic_ports.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: include_directories('..'),
)
test('basic_ports', test_filter_basic_ports, suite: ['filter'])

# test basic net4
test_filter_basic_net4 = executable('basic_net4',
['basic_net4.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_net4', test_filter_basic_net4, suite: ['filter'])

# test basic net4 fast
test_filter_basic_net4_fast = executable('basic_net4_fast',
['basic_net4_fast.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_net4_fast', test_filter_basic_net4_fast, suite: ['filter'])

# test basic net6 fast
test_filter_basic_net6_fast = executable('basic_net6_fast',
['basic_net6_fast.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_net6_fast', test_filter_basic_net6_fast, suite: ['filter'])

# test basic net6
test_filter_basic_net6 = executable('basic_net6',
['basic_net6.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_net6', test_filter_basic_net6, suite: ['filter'])

# test corner cases
test_filter_corner = executable(
    'corner', 
  ['corner.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('corner', test_filter_corner, suite: ['filter'])

# test net4 + ports filter
test_filter_net4_ports = executable(
    'net4_ports', 
  ['net4_ports.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('net4_ports', test_filter_net4_ports, suite: ['filter'])

# test memory reuse
test_filter_memory = executable('memory',
  ['memory.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('memory', test_filter_memory, suite: ['filter'])

# test proto
test_filter_proto = executable('basic_proto',
  ['basic_proto.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto', test_filter_proto, suite: ['filter'])

# test vlan
test_filter_vlan = executable('basic_vlan',
  ['basic_vlan.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_vlan', test_filter_vlan, suite: ['filter'])

# test filter macros
filter_macros = executable(
  'macros',
  ['macros.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: include_directories('..'),
)
test('macros', filter_macros, suite: ['filter'])

basic_port_fast = executable('basic_port_fast',
  ['basic_port_fast.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_port_fast', basic_port_fast, suite: ['filter'])

basic_proto_range = executable('basic_proto_range',
  ['basic_proto_range.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto_range', basic_proto_range, suite: ['filter'])


basic_proto_range_fast = executable('basic_proto_range_fast',
  ['basic_proto_range_fast.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto_range_fast', basic_proto_range_fast, suite: ['filter'])

# test combo net4_fast_src + port_fast_src
combo_net4_port_src = executable('combo_net4_port_src',
  ['combo_net4_port_src.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('combo_net4_port_src', combo_net4_port_src, suite: ['filter'])

# test combo net6_fast_src + port_fast_src
combo_net6_port_src = executable('combo_net6_port_src',
  ['combo_net6_port_src.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('combo_net6_port_src', combo_net6_port_src, suite: ['filter'])

# test combo net4_fast_dst + port_fast_dst + proto_range
combo_net4_port_proto_dst = executable('combo_net4_port_proto_dst',
  ['combo_net4_port_proto_dst.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('combo_net4_port_proto_dst', combo_net4_port_proto_dst, suite: ['filter'])

subdir('shm')

executable('bench_net4',
  ['bench_net4.c'], 
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

executable('bench_net4_fast',
  ['bench_net4_fast.c'], 
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

executable('bench_net6',
  ['bench_net6.c'], 
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

executable('bench_net6_fast',
  ['bench_net6_fast.c'], 
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
