cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_errors_dep,
  lib_fwstate_dep,
  libdpdk_inc_dep,
]

includes = include_directories('../../..')

sources = files(
  'fwstate_map_object.c',
)

lib_fwstate_objects = static_library(
  'fwstate_objects',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_fwstate_objects_dep = declare_dependency(
  link_with: [lib_fwstate_objects],
  include_directories: include_directories('.'),
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_object_fwstate_map_v4=new_object_fwstate_map_v4',
    '-Wl,--export-dynamic-symbol=new_object_fwstate_map_v4',
    '-Wl,--defsym',
    '-Wl,new_object_fwstate_map_v6=new_object_fwstate_map_v6',
    '-Wl,--export-dynamic-symbol=new_object_fwstate_map_v6',
  ],
)
