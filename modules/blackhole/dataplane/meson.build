dp_dependencies = [
  lib_common_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_blackhole_dp = static_library(
  'blackhole_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_blackhole_dp_dep = declare_dependency(
  link_with: lib_blackhole_dp,
  link_args: [
    '-Wl,--require-defined=new_module_blackhole',
    '-Wl,--export-dynamic-symbol=new_module_blackhole',
  ],
  dependencies: dp_dependencies,
)
