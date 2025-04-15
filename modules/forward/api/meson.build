# controlplane dependencies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep

includes = include_directories('../dataplane')

sources = files(
  'controlplane.c',
)

lib_forward_cp = static_library(
  'forward_cp',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_forward_cp_dep = declare_dependency(
  link_with: lib_forward_cp,
)
