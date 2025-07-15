test_sources = ['util.c']

test_args = [
  '-O0',
  '-g'
]

test_deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, libdpdk_inc_dep, libdpdk_dep]

# test basic ports

test_filter_basic_ports = executable(
    'test_filter_basic_ports', 
  test_sources + ['basic_ports.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('basic_ports', test_filter_basic_ports, suite: ['filter'])

# test basic net4

test_filter_basic_net4 = executable('test_filter_basic_net4',
  test_sources + ['basic_net4.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('basic_net4', test_filter_basic_net4, suite: ['filter'])

# test corner cases

test_filter_corner = executable(
    'test_filter_corner', 
  test_sources + ['corner.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('corner', test_filter_corner, suite: ['filter'])

# test net4 + ports filter
test_filter_net4_ports = executable(
    'test_filter_net4_ports', 
  test_sources + ['net4_ports.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('net4_ports', test_filter_net4_ports, suite: ['filter'])

# test memory reuse

test_filter_memory = executable('test_filter_memory',
  test_sources + ['memory.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('memory', test_filter_memory, suite: ['filter'])

# test proto
test_filter_proto = executable('test_filter_proto',
  test_sources + ['basic_proto.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto', test_filter_proto, suite: ['filter'])

# test vlan
test_filter_vlan = executable('test_filter_vlan',
  test_sources + ['basic_vlan.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_vlan', test_filter_vlan, suite: ['filter'])

# bench and compare vs previous filter
test_vs_prev = executable('filter_bench',
 test_sources + ['bench.c'],
  c_args: yanet_c_args + ['-O3'],
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('bench', test_vs_prev, suite: ['filter'])