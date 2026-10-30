classify_smoke = executable(
  'classify_smoke',
  ['smoke.c'],
  dependencies: [lib_common_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)

test('classify_smoke', classify_smoke, suite: ['filter'])

classify_net6 = executable(
  'classify_net6',
  ['net6.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, lib_lib_utils_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)

test('classify_net6', classify_net6, suite: ['filter'])

classify_core = executable(
  'classify_core',
  ['core.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, lib_lib_utils_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)

test('classify_core', classify_core, suite: ['filter'])

classify_fragment = executable(
  'classify_fragment',
  ['fragment.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)

test('classify_fragment', classify_fragment, suite: ['filter'])

classify_context_names = executable(
  'classify_context_names',
  ['context_names.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, lib_logging_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)

test('classify_context_names', classify_context_names, suite: ['filter'])

classify_join_test = executable(
  'classify_join_test',
  ['join.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)
test('classify_join_test', classify_join_test, suite: ['filter'])

classify_net_hash = executable(
  'classify_net_hash',
  ['net_hash.c'],
  c_args: yanet_test_c_args,
  dependencies: [lib_common_dep, lib_classify_compiler_dep, libdpdk_inc_dep],
  include_directories: yanet_rootdir,
)
test('classify_net_hash', classify_net_hash, suite: ['filter'])
