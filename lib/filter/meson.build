filter_compiler_sources = files(
  'compiler/helper.c',
  'compiler/device.c',
  'compiler/net4.c',
  'compiler/net6.c',
  'compiler/port.c',
  'compiler/proto_range.c',
  'compiler/vlan.c',
  'compiler/ipfrag.c',
)

lib_filter_compiler = static_library(
  'filter_compiler',
  filter_compiler_sources,
  dependencies: [lib_common_dep, lib_errors_dep],
  install: false,
)

lib_filter_compiler_dep = declare_dependency(
  link_with: lib_filter_compiler,
  dependencies: [lib_common_dep, lib_errors_dep],
)

subdir('tests')
