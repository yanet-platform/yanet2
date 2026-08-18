lib_test_plugin_zero_count = shared_module(
  'zerocountabihashtest_dp',
  files('zero_count.c'),
  c_args: yanet_c_args + ['-fvisibility=hidden'],
  link_args: yanet_link_args,
  dependencies: [lib_module_dp_dep],
  install: false,
  build_by_default: false,
)

abi_test_zero_count_plugin_dir = meson.current_build_dir()
