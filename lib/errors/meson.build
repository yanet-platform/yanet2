sources = files(
  'errors.c',
)

lib_errors = static_library(
  'errors',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir],
  install: false,
)

lib_errors_dep = declare_dependency(
  link_with: lib_errors,
  include_directories: [include_directories('.'), yanet_rootdir],
)
