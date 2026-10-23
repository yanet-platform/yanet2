dependencies = [
  lib_common_dep,
  lib_packet_dp_dep,
]

sources = files(
  'module.c',
)

lib_module_dp = static_library(
  'module',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_module_dp_dep = declare_dependency(
  link_with: [lib_module_dp],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
