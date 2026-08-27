sources = files(
  'cancellation.c',
)

lib_cancellation = static_library(
  'cancellation',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  install: false,
)

lib_cancellation_dep = declare_dependency(
  link_with: lib_cancellation,
  include_directories: yanet_rootdir,
)
