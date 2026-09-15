lib_filter2_query_dep = declare_dependency(
  include_directories: include_directories('.'),
)

compiler_sources = files(
  'compiler/helper.c',
)

lib_filter2_compiler = static_library(
  'filter2_compiler',
  compiler_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_common_dep, lib_errors_dep],
  install: false,
)

lib_filter2_compiler_dep = declare_dependency(
  link_with: lib_filter2_compiler,
  include_directories: include_directories('.'),
  dependencies: [lib_errors_dep],
)

subdir('tests')
