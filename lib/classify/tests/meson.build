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

# The sparse form of the value tables is exercised by lowering the
# dense budget for these targets: the unit test against a dense
# oracle, and a copy of the core composition test with every joint
# forced sparse.
classify_value_table = executable(
  'classify_value_table',
  ['value_table.c'],
  c_args: yanet_test_c_args + ['-DVALUE_TABLE_DENSE_MAX_CELLS=1024'],
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_value_table', classify_value_table, suite: ['filter'])

# The joints live in the prebuilt classify compiler library, so the
# forced sparse define must reach that translation unit too: the
# variant library below compiles the same sources with the threshold
# zeroed, and the sparse core test links it instead of the stock one.
# The chunk size drops alongside so the smallest joints of the test
# arena do not pay a full chunk each under the sanitizers.
classify_compiler_sparse = static_library(
  'classify_compiler_sparse',
  compiler_sources,
  c_args: yanet_test_c_args + [
    '-DVALUE_TABLE_DENSE_MAX_CELLS=0',
    '-DVALUE_TABLE_SPARSE_CHUNK_BITS=10',
  ],
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_errors_dep],
  install: false,
)

classify_compiler_sparse_dep = declare_dependency(
  link_with: classify_compiler_sparse,
  include_directories: include_directories('..'),
  dependencies: [lib_errors_dep],
)

sparse_test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  classify_compiler_sparse_dep,
  lib_classify_query_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

classify_core_sparse = executable(
  'classify_core_sparse',
  ['core.c'],
  c_args: yanet_test_c_args + [
    '-DVALUE_TABLE_DENSE_MAX_CELLS=0',
    '-DVALUE_TABLE_SPARSE_CHUNK_BITS=10',
  ],
  link_args: yanet_link_args,
  dependencies: sparse_test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('classify_core_sparse', classify_core_sparse, suite: ['filter'])
