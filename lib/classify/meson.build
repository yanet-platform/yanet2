lib_classify_query_dep = declare_dependency(
  include_directories: include_directories('.'),
)

compiler_sources = files(
  'compiler/helper.c',
)

lib_classify_compiler = static_library(
  'classify_compiler',
  compiler_sources,
  dependencies: [lib_common_dep],
  install: false,
)

lib_classify_compiler_dep = declare_dependency(
  link_with: lib_classify_compiler,
  include_directories: include_directories('.'),
  dependencies: [lib_common_dep],
)

subdir('tests')
