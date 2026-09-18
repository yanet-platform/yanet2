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

# acl module benchmarks: the production path against the shared core
# path over a dumped ruleset and a capture. Not registered as tests -
# they run against operator supplied data; see bench/dump_acl_ruleset.py.
# Each binary links exactly one filter library: both export the same
# join helper symbol, so a shared dependency list would collide.
bench_common_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_lib_utils_dep,
  libdpdk_inc_dep,
  libdpdk_dep,
]

executable(
  'bench_acl_production',
  ['bench/bench_acl_production.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_filter_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)

executable(
  'bench_acl_core',
  ['bench/bench_acl_core.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_classify_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)

# forward module benchmarks, same shape as the acl pair above.
executable(
  'bench_forward_production',
  ['bench/bench_forward_production.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_filter_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)

executable(
  'bench_forward_classify',
  ['bench/bench_forward_classify.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_classify_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)
