lib_test_plugin_unknown = shared_module(
  'unknownabihashtest_dp',
  files('unknown.c'),
  c_args: yanet_c_args + ['-fvisibility=hidden'],
  link_args: yanet_link_args,
  dependencies: [lib_module_dp_dep],
  install: false,
  build_by_default: false,
)

abi_test_unknown_plugin_dir = meson.current_build_dir()
