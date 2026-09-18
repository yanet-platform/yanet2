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

filter2_net6 = executable(
  'filter2_net6',
  ['net6.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('filter2_net6', filter2_net6, suite: ['filter'])

filter2_core = executable(
  'filter2_core',
  ['core.c'],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: test_deps,
  link_language: 'c',
  include_directories: yanet_rootdir,
)

test('filter2_core', filter2_core, suite: ['filter'])

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

bench_acl_production = executable(
  'bench_acl_production',
  ['bench/bench_acl_production.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_filter_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)

bench_acl_core = executable(
  'bench_acl_core',
  ['bench/bench_acl_core.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [bench_common_deps, lib_filter2_compiler_dep],
  link_language: 'c',
  include_directories: include_directories('.'),
)
