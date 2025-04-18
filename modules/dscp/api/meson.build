# common dependencies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep

includes = include_directories('../dataplane')

api_sources = files(
  'controlplane.c',
)

lib_dscp_cp = static_library(
  'dscp_cp',
  api_sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_dscp_cp_dep = declare_dependency(
  link_with: lib_dscp_cp,
)
