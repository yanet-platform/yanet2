lib_tls_stack = static_library(
  'tls_stack',
  ['stack.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  install: false,
)

lib_tls_stack_dep = declare_dependency(
  link_with: lib_tls_stack,
  include_directories: include_directories('.'),
)