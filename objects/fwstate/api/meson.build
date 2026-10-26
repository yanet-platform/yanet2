cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_errors_dep,
  lib_statemap_dep,
]

sources = files(
  'fwstate_map_object.c',
)

lib_fwstate_objects = static_library(
  'fwstate_objects',
  sources,
  dependencies: cp_dependencies + [lib_fwstate_registry_dep],
  install: false,
)

lib_fwstate_objects_dep = declare_dependency(
  link_with: [lib_fwstate_objects],
  link_args: [
    '-Wl,--require-defined=new_object_fwstate_map_v4',
    '-Wl,--export-dynamic-symbol=new_object_fwstate_map_v4',
    '-Wl,--require-defined=new_object_fwstate_map_v6',
    '-Wl,--export-dynamic-symbol=new_object_fwstate_map_v6',
  ],
  dependencies: cp_dependencies,
)

dataplane_object_deps += lib_fwstate_objects_dep
