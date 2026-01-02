dependencies = []

dependencies += lib_common_dep
dependencies += lib_packet_dp_dep
dependencies += libdpdk_inc_dep

sources = files(
  'device.c',
)

lib_device_dp = static_library(
  'device',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_device_dp_dep = declare_dependency(
  link_with: lib_device_dp,
  dependencies: dependencies,
  include_directories: [
    yanet_libdir,
    yanet_rootdir,
  ],
)
