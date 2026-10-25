dp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_route_dp = static_library(
  'route_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_route_dp_dep = declare_dependency(
  link_with: lib_route_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_route',
    '-Wl,--export-dynamic-symbol=new_module_route',
    '-Wl,--require-defined=new_object_route_fib',
    '-Wl,--export-dynamic-symbol=new_object_route_fib',
  ],
)
