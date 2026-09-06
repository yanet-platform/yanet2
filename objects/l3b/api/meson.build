cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_errors_dep,
  lib_filter_compiler_dep,
  lib_fwstate_dep,
]

includes = include_directories('../../..')

sources = files(
  'l3b_session_table_object.c',
  'l3b_virtual_service_object.c',
)

lib_l3b_objects = static_library(
  'l3b_objects',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_l3b_objects_dep = declare_dependency(
  link_with: [lib_l3b_objects],
  include_directories: include_directories('.'),
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_object_l3b_virtual_service=new_object_l3b_virtual_service',
    '-Wl,--export-dynamic-symbol=new_object_l3b_virtual_service',
    '-Wl,--defsym',
    '-Wl,new_object_l3b_session_table=new_object_l3b_session_table',
    '-Wl,--export-dynamic-symbol=new_object_l3b_session_table',
  ],
)
