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

# test non-terminative actions
non_term_actions = executable('non_term_actions',
  ['non_term.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('non_term_actions', non_term_actions, suite: ['filter'])

category = executable('category',
  ['category.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('category', category, suite: ['filter'])

basic_proto_range = executable('basic_proto_range',
  ['basic_proto_range.c'], 
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)
test('basic_proto_range', basic_proto_range, suite: ['filter'])

subdir('shm')
