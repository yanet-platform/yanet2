# controlplane dependencies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep
cp_dependencies += lib_logging_dep

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
)
