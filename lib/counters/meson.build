dependencies = [
  lib_common_dep,
]

sources = files(
  'counters.c',
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

