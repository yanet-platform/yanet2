dependencies = [
  lib_module_dp_dep,
  lib_pipeline_dp_dep,
]

sources = files(
  'zone.c',
)

lib_config_cp = static_library(
  'config_cp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_config_cp_dep = declare_dependency(
  link_with: lib_config_cp,
  include_directories: yanet_libdir,
)
