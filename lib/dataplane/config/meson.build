dependencies = []

dependencies += lib_module_dp_dep
dependencies += lib_pipeline_dp_dep

sources = files(
  'dataplane_registry.c',
)

lib_config_dp = static_library(
  'config',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_config_dp_dep = declare_dependency(
  link_with: lib_config_dp,
  include_directories: yanet_libdir,
)
