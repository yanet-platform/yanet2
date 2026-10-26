dp_dependencies = [
  lib_common_dep,
  libdpdk_inc_dep,
]

dp_sources = files(
  'nat64dp.c',
)

lib_nat64_dp = static_library(
  'nat64_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_nat64_dp_dep = declare_dependency(
  link_with: lib_nat64_dp,
  dependencies: dp_dependencies,
  link_args: [
    '-Wl,--require-defined=new_module_nat64',
    '-Wl,--export-dynamic-symbol=new_module_nat64',
  ],
)

dataplane_module_deps += lib_nat64_dp_dep
