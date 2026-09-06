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
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_statemap_dep = declare_dependency(
  link_with: lib_statemap,
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
