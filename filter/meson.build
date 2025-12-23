lib_filter_query_dep = declare_dependency(
  include_directories: include_directories('.'),
)

compiler_sources = files(
  'compiler/helper.c',
  'compiler/device.c',
  'compiler/net4.c',
  'compiler/net6.c',
  'compiler/port.c',
  'compiler/proto_range.c',
  'compiler/proto.c',
  'compiler/vlan.c',
)

lib_filter_compiler = static_library(
  'filter_compiler',
  compiler_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep],
  install: false,
)

lib_filter_compiler_dep = declare_dependency(
  link_with: lib_filter_compiler,
  include_directories: include_directories('.'), 
)

subdir('tests')
