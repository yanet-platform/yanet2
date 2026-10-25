dp_dependencies = [
  lib_common_dep,
  lib_fwstate_dep,
  lib_fwstate_objects_dep,
  lib_worker_dp_dep,
  lib_logging_dep,
  lib_statemap_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_fwstate_dp = static_library(
  'fwstate_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_fwstate_dp_dep = declare_dependency(
  link_with: lib_fwstate_dp,
  link_args: [
    '-Wl,--require-defined=new_module_fwstate',
    '-Wl,--export-dynamic-symbol=new_module_fwstate',
  ],
  dependencies: dp_dependencies,
)
