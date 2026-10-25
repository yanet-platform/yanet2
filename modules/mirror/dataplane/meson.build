dp_dependencies = [
  lib_common_dep,
  lib_worker_dp_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_mirror_dp = static_library(
  'mirror_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_mirror_dp_dep = declare_dependency(
  link_with: lib_mirror_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_mirror',
    '-Wl,--export-dynamic-symbol=new_module_mirror',
  ],
)
