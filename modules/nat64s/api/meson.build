cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_logging_dep,
  lib_config_cp_dep,
  lib_agent_cp_dep,
  lib_fwstate_objects_dep,
]

sources = files(
  'controlplane.c',
)

lib_nat64s_cp = static_library(
  'nat64s_cp',
  sources,
  dependencies: cp_dependencies,
  install: false,
)

lib_nat64s_cp_dep = declare_dependency(
  link_with: lib_nat64s_cp,
  dependencies: cp_dependencies,
)
