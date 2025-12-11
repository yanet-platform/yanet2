lib_diag = static_library(
  'diag',
  ['diag.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir],
  install: false,
)

lib_diag_dep = declare_dependency(
  link_with: lib_diag,
  include_directories: include_directories('.'),
)