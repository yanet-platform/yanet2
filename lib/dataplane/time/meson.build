dependencies = [
  libdpdk_inc_dep,
]


sources = files(
  'clock.c',
)

lib_time_dp = static_library(
  'time_dp',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_time_dp_dep = declare_dependency(
  link_with: [lib_time_dp],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
