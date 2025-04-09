dependencies = []

dependencies += lib_common_dep
dependencies += lib_packet_dp_dep

sources = files(
  'module.c',
)

lib_module_dp = static_library(
  'module',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_module_dp_dep = declare_dependency(
  link_with: lib_module_dp,
  include_directories: [
    yanet_libdir,
    yanet_rootdir,
  ],
)

testing_sources = sources + files('testing.c')

lib_testing_module_dp = static_library(
  'testing_module',
  testing_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_testing_module_dp_dep = declare_dependency(
  link_with: lib_testing_module_dp,
  include_directories: yanet_libdir,
)
