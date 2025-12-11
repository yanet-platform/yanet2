lib_diag = static_library(
  'diag',
  ['diag.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  include_directories: [yanet_rootdir],
  install: false,
  dependencies: [lib_tls_stack_dep],
)

lib_diag_dep = declare_dependency(
  link_with: lib_diag,
  include_directories: [include_directories('.'), yanet_rootdir],
  dependencies: [lib_tls_stack_dep],
)

diag_test = executable('diag_test', ['test.c'], dependencies: [lib_diag_dep, lib_tls_stack_dep], include_directories: [yanet_rootdir])
test('diag_test', diag_test, suite: ['diag'])
