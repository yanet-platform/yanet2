lib_test_plugin_no_count = shared_module(
  'nocountabihashtest_dp',
  files('no_count.c'),
  c_args: yanet_c_args + ['-fvisibility=hidden'],
  link_args: yanet_link_args,
  dependencies: [lib_module_dp_dep],
  install: false,
  build_by_default: false,
)

abi_test_no_count_plugin_dir = meson.current_build_dir()
