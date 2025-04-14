# common dependencies
dependencies += lib_common_dep

# dataplane dependencies
dp_dependencies = dependencies
dp_dependencies += lib_packet_dp_dep
dp_dependencies += lib_module_dp_dep

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
  link_args: [
    '-Wl,--defsym', '-Wl,new_module_nat64=new_module_nat64',
    '-Wl,--export-dynamic-symbol=new_module_nat64',
  ],
)

# controlplane dependencies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep
cp_dependencies += lib_logging_dep

sources = files(
  'nat64cp.c',
)

lib_nat64_cp = static_library(
  'nat64_cp',
  sources,
  dependencies: cp_dependencies,
  install: false,
)

lib_nat64_cp_dep = declare_dependency(
  link_with: lib_nat64_cp,
)

subdir('unit')