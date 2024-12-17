dependencies = []

dependencies += lib_packet_dp_dep

sources = files(
  'module.c',
  'module_registry.c',
  'module_config_registry.c',
)

lib_module_dp = static_library(
  'module',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_module_dp_dep = declare_dependency(
  link_with: lib_module_dp,
  include_directories: yanet_libdir,
)
