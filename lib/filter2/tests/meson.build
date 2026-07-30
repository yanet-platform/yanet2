test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_filter2_compiler_dep,
  lib_filter2_query_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

filter2_smoke = executable(
  'filter2_smoke',
  ['smoke.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('filter2_smoke', filter2_smoke, suite: ['filter'])
