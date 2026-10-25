cp_dependencies = [
    lib_common_dep,
    lib_errors_dep,
    lib_config_cp_dep,
]

sources = files(
  'controlplane.c',
)

lib_blackhole_cp = static_library(
  'blackhole_cp',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_blackhole_cp_dep = declare_dependency(
  link_with: [lib_blackhole_cp],
  dependencies: cp_dependencies,
)
