cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_logging_dep,
  lib_config_cp_dep,
]

includes = include_directories('../dataplane')

sources = files(
  'nat64cp.c',
)

lib_nat64_cp = static_library(
  'nat64_cp',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_nat64_cp_dep = declare_dependency(
  link_with: lib_nat64_cp,
  dependencies: cp_dependencies,
)
