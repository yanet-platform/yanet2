# StateMap library - the generic layered hash map core shared by the
# domain-specific state libraries (fwstate, l3state)
dependencies = [
    lib_common_dep,
]

sources = files(
  'fwmap.c',
)

lib_statemap = static_library(
  'statemap',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_statemap_dep = declare_dependency(
  link_with: lib_statemap,
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
