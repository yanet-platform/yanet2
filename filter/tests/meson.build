test_filter_sources = ['test.c', 'util.c']

test_args = [
  '-O0',
  '-g'
]

executable(
    'test_filter', 
  test_filter_sources,
  c_args: test_args, 
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, libdpdk_inc_dep, libdpdk_dep],
  link_language: 'c'
)