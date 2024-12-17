dependencies = []

dependencies += lib_common_dep
dependencies += lib_filter_dep

lib_sources = files(
    'module.c')

lib_controlplane = static_library(
  'controlplane',
  lib_sources,
  dependencies: dependencies,
  install: false,
)

lib_controlplane_dep = declare_dependency(
  link_with: lib_controlplane,
  include_directories: include_directories('.'),
)

dependencies += lib_controlplane_dep

sources = files(
  'main.c',
)

executable(
  'yanet-controlplane',
  sources,
  dependencies: dependencies,
  install: true,
)

