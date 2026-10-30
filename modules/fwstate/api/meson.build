cp_dependencies = [
  lib_common_dep,
  lib_classify_compiler_dep,
  lib_errors_dep,
  lib_config_cp_dep,
  lib_counters_dep,
]

sources = files(
  'fwstate_cp.c',
)

lib_fwstate_cp = static_library(
  'fwstate_cp',
  sources,
  dependencies: cp_dependencies,
  install: false,
)

lib_fwstate_cp_dep = declare_dependency(
  link_with: [lib_fwstate_cp, lib_classify_compiler],
  dependencies: cp_dependencies,
)
