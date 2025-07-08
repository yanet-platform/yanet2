test_sources = ['util.c']

test_args = [
  '-O0',
  '-g'
]

test_deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, libdpdk_inc_dep, libdpdk_dep]

test_filter_basic1 = executable(
    'test_filter_basic1', 
  test_sources + ['filter_basic1.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('basic1', test_filter_basic1, suite: ['filter'])

test_filter_corner = executable(
    'test_filter_corner', 
  test_sources + ['filter_corner.c'],
  c_args: test_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c'
)

test('corner', test_filter_corner, suite: ['filter'])

