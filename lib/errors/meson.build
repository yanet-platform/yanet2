sources = files(
  'errors.c',
)

lib_errors = static_library(
  'errors',
  sources,
  include_directories: [yanet_rootdir],
  install: false,
)

lib_errors_dep = declare_dependency(
  link_with: lib_errors,
  include_directories: [yanet_rootdir],
)
