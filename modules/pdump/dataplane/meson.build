dp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_pdump_dp = static_library(
  'pdump_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_pdump_dp_dep = declare_dependency(
  link_with: lib_pdump_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_pdump=new_module_pdump',
    '-Wl,--export-dynamic-symbol=new_module_pdump',
  ],
)
