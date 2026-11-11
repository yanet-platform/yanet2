test_deps = [
  lib_common_dep,
  lib_packet_dp_dep,
  lib_filter_compiler_dep,
  lib_lib_utils_dep,
  lib_errors_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

# test basic ports
test_filter_basic_ports = executable(
  'basic_ports',
  ['basic_ports.c'],
  dependencies: test_deps,
)
test('basic_ports', test_filter_basic_ports, suite: ['filter'])

# test basic net4
test_filter_basic_net4 = executable('basic_net4',
['basic_net4.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('basic_net4', test_filter_basic_net4, suite: ['filter'])

# test basic net6
test_filter_basic_net6 = executable('basic_net6',
['basic_net6.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('basic_net6', test_filter_basic_net6, suite: ['filter'])

# test corner cases
test_filter_corner = executable(
    'corner', 
  ['corner.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('corner', test_filter_corner, suite: ['filter'])

# test net4 + ports filter
test_filter_net4_ports = executable(
    'net4_ports', 
  ['net4_ports.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('net4_ports', test_filter_net4_ports, suite: ['filter'])

test_filter_net6_overlap = executable('net6_overlap',
  ['net6_overlap.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('net6_overlap', test_filter_net6_overlap, suite: ['filter'])

# test memory reuse
test_filter_memory = executable('memory',
  ['memory.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('memory', test_filter_memory, suite: ['filter'])

# test memory_context node naming
test_filter_context_names = executable('context_names',
  ['context_names.c'],
  c_args: yanet_test_c_args,
  dependencies: [
    lib_common_dep,
    lib_filter_compiler_dep,
    lib_errors_dep,
    lib_logging_dep,
    libdpdk_inc_dep,
  ],
)
test('context_names', test_filter_context_names, suite: ['filter'])

# test vlan
test_filter_vlan = executable('basic_vlan',
  ['basic_vlan.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('basic_vlan', test_filter_vlan, suite: ['filter'])

# test filter macros
filter_macros = executable(
  'macros',
  ['macros.c'],
  dependencies: test_deps,
)
test('macros', filter_macros, suite: ['filter'])

basic_proto_range = executable('basic_proto_range',
  ['basic_proto_range.c'],
  c_args: yanet_test_c_args,
  dependencies: test_deps,
)
test('basic_proto_range', basic_proto_range, suite: ['filter'])

subdir('shm')

# Run by hand only: each maps about 11 GiB of hugepages (some 5,500 2 MiB pages).
executable('bench_net4',
  ['bench_net4.c'], 
  dependencies: test_deps,
)

executable('bench_net6',
  ['bench_net6.c'],
  dependencies: test_deps,
)

# Allocation-failure injection sweep. The compiler sources are recompiled
# with oom_shim.h force-included so every memory_balloc call on the compile
# path can be forced to fail one at a time.
oom_shim_c_args = yanet_test_c_args + [
  '-include', meson.current_source_dir() / 'oom_shim.h',
]

filter_compiler_oom_shim = static_library(
  'filter_compiler_oom_shim',
  filter_compiler_sources,
  c_args: oom_shim_c_args,
  dependencies: [lib_common_dep, lib_errors_dep],
  install: false,
)

filter_compiler_oom_shim_dep = declare_dependency(
  link_with: filter_compiler_oom_shim,
  dependencies: [lib_common_dep, lib_errors_dep],
)

oom_sweep_deps = [
  lib_common_dep,
  filter_compiler_oom_shim_dep,
  lib_errors_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

# The sweep test itself inlines part of the compile path (filter_init is a
# static inline in compiler.h), so it needs the same shim.
test_filter_oom_sweep = executable('oom_sweep',
  ['oom_sweep.c'],
  c_args: oom_shim_c_args,
  dependencies: oom_sweep_deps,
)
test('oom_sweep', test_filter_oom_sweep, suite: ['filter'])
