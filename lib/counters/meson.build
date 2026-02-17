dependencies = [
  lib_common_dep,
]

sources = files(
  'counters.c',
  'histogram.c'
)

lib_counters = static_library(
  'counters',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_counters_dep = declare_dependency(
  link_with: [lib_counters],
  include_directories: yanet_libdir,
  dependencies: dependencies,
)

# Tests
test_counters_histogram = executable(
  'counters_test',
  'tests.c',
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [lib_counters_dep, lib_common_dep, lib_logging_dep],
  include_directories: yanet_rootdir,
  link_language: 'c',
)
test('counters', test_counters_histogram, suite: ['lib'])

