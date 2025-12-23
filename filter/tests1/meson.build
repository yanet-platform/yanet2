test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_filter_compiler_dep,
  lib_filter_query_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

# filter macros test
test_filter_macros = executable(
  'macros',
  ['macros.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: include_directories('..'),
)
test('macros', test_filter_macros, suite: ['filter'])

# filter basic_ports test
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