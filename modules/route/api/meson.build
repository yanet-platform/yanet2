cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_config_cp_dep,
  lib_counters_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
  'fib.c',
  'fib_object.c',
)

lib_route_cp = static_library(
  'route_cp',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_route_cp_dep = declare_dependency(
  link_with: lib_route_cp,
  dependencies: cp_dependencies,
)
