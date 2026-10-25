dp_dependencies = [
  lib_common_dep,
  lib_ring_objects_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_pdump_dp = static_library(
  'pdump_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_pdump_dp_dep = declare_dependency(
  link_with: lib_pdump_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_pdump',
    '-Wl,--export-dynamic-symbol=new_module_pdump',
  ],
)
