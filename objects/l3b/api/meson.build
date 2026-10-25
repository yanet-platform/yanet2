cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_errors_dep,
  lib_classify_compiler_dep,
  lib_statemap_dep,
]

includes = include_directories('../../..')

sources = files(
  'l3b_session_table_object.c',
  'l3b_virtual_service_object.c',
)

lib_l3b_objects = static_library(
  'l3b_objects',
  sources,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_l3b_objects_dep = declare_dependency(
  link_with: [lib_l3b_objects],
  include_directories: include_directories('.'),
  link_args: [
    '-Wl,--require-defined=new_object_l3b_virtual_service',
    '-Wl,--export-dynamic-symbol=new_object_l3b_virtual_service',
    '-Wl,--require-defined=new_object_l3b_session_table',
    '-Wl,--export-dynamic-symbol=new_object_l3b_session_table',
  ],
  dependencies: cp_dependencies,
)
