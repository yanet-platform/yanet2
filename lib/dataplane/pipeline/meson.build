dependencies = [
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

sources = files(
  'pipeline.c',
)

lib_pipeline_dp = static_library(
  'pipeline',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_pipeline_dp_dep = declare_dependency(
  link_with: [lib_pipeline_dp],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
