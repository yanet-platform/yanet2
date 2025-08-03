test_args = [
  '-O0',
  '-g'
]

test_deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, lib_filter_utils_dep, libdpdk_inc_dep, libdpdk_dep]

# test basic ports
test_filter_basic_ports = executable(
    'basic_ports', 
  ['basic_ports.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_ports', test_filter_basic_ports, suite: ['filter'])

# test basic net4
test_filter_basic_net4 = executable('basic_net4',
  
['basic_net4.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_net4', test_filter_basic_net4, suite: ['filter'])

# test corner cases
test_filter_corner = executable(
    'corner', 
  ['corner.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('corner', test_filter_corner, suite: ['filter'])

# test net4 + ports filter
test_filter_net4_ports = executable(
    'net4_ports', 
  ['net4_ports.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('net4_ports', test_filter_net4_ports, suite: ['filter'])

# test memory reuse
test_filter_memory = executable('memory',
  ['memory.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('memory', test_filter_memory, suite: ['filter'])

# test proto
test_filter_proto = executable('basic_proto',
  ['basic_proto.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto', test_filter_proto, suite: ['filter'])

# test vlan
test_filter_vlan = executable('basic_vlan',
  ['basic_vlan.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_vlan', test_filter_vlan, suite: ['filter'])

# bench and compare with previous filter
test_vs_prev = executable('new_vs_old',
 ['vs_prev.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('vs_prev', test_vs_prev, suite: ['filter'])

# test filter macros
filter_macros = executable('macros',
 ['macros.c'],
  c_args: yanet_c_args + ['-O3'],
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('macros', filter_macros, suite: ['filter'])