dependencies = []

dependencies += lib_common_dep

sources = files(
  'ipfw.c',
)

lib_filter = static_library(
  'filter',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_filter_dep = declare_dependency(
  link_with: lib_filter,
  include_directories: include_directories('.'),
)
