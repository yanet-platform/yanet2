dp_dependencies = [
  lib_common_dep,
  lib_logging_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
]

dp_sources = files(
  'nat64dp.c',
)

lib_nat64_dp = static_library(
  'nat64_dp',
  dp_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dp_dependencies,
  install: false,
)

lib_nat64_dp_dep = declare_dependency(
  link_with: lib_nat64_dp,
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_module_nat64=new_module_nat64',
    '-Wl,--export-dynamic-symbol=new_module_nat64',
  ],
)
