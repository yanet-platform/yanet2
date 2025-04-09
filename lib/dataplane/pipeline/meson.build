dependencies = []

dependencies += lib_packet_dp_dep
dependencies += lib_module_dp_dep

sources = files(
  'pipeline.c',
)

lib_pipeline_dp = static_library(
  'pipeline',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_pipeline_dp_dep = declare_dependency(
  link_with: lib_pipeline_dp,
  include_directories: yanet_libdir,
)
