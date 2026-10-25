cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_classify_compiler_dep,
  lib_config_cp_dep,
  lib_counters_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_mirror_cp = static_library(
  'mirror_cp',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_mirror_cp_dep = declare_dependency(
  link_with: lib_mirror_cp,
  dependencies: cp_dependencies,
)
