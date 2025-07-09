test_sources = ['util.c']

test_args = [
  '-O0',
  '-g'
]

test_deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, libdpdk_inc_dep, libdpdk_dep]

# test basic ports

test_filter_basic_ports = executable(
    'test_filter_basic_ports', 
  test_sources + ['filter_basic_ports.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('filter_basic_ports', test_filter_basic_ports, suite: ['filter'])

# test basic net4

test_filter_basic_net4 = executable('test_filter_basic_net4',
  test_sources + ['filter_basic_net4.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('filter_basic_net4', test_filter_basic_net4, suite: ['filter'])

# test corner cases

test_filter_corner = executable(
    'test_filter_corner', 
  test_sources + ['filter_corner.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('filter_corner', test_filter_corner, suite: ['filter'])

