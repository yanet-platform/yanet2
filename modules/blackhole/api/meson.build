cp_dependencies = [
    lib_counters_dep,
    lib_common_dep
]

sources = files(
  'controlplane.c',
)

lib_blackhole_cp = static_library(
  'blackhole_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_blackhole_cp_dep = declare_dependency(
  link_with: [lib_blackhole_cp],
)
