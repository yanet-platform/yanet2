dp_dependencies = [
  lib_common_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_dev_plain_dp = static_library(
  'plain_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_dev_plain_dp_dep = declare_dependency(
  link_with: lib_dev_plain_dp,
  link_args: [
    '-Wl,--require-defined=new_device_plain',
    '-Wl,--export-dynamic-symbol=new_device_plain',
  ],
  dependencies: dp_dependencies,
)

dataplane_device_deps += lib_dev_plain_dp_dep
