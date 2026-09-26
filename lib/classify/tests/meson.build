test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_classify_compiler_dep,
  lib_classify_query_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

classify_smoke = executable(
  'classify_smoke',
  ['smoke.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_smoke', classify_smoke, suite: ['filter'])

classify_net6 = executable(
  'classify_net6',
  ['net6.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_net6', classify_net6, suite: ['filter'])

classify_core = executable(
  'classify_core',
  ['core.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_core', classify_core, suite: ['filter'])

classify_fragment = executable(
  'classify_fragment',
  ['fragment.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_fragment', classify_fragment, suite: ['filter'])
