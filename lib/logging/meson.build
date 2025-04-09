sources = files(
  'log.c',
)

lib_logging = static_library(
  'logging',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  install: false,
)

lib_logging_dep = declare_dependency(
  link_with: lib_logging,
  include_directories: yanet_libdir,
)
