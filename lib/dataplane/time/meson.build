dependencies = [
  libdpdk_inc_dep,
]


sources = files(
  'clock.c',
)

lib_time_dp = static_library(
  'time_dp',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_time_dp_dep = declare_dependency(
  link_with: lib_time_dp,
  include_directories: [
    yanet_libdir,
    yanet_rootdir,
  ],
)
