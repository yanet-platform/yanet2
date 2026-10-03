cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_errors_dep,
  libdpdk_inc_dep,
]

includes = include_directories('../../..')

sources = files(
  'ring_object.c',
)

lib_ring_objects = static_library(
  'ring_objects',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_ring_objects_dep = declare_dependency(
  link_with: [lib_ring_objects],
  include_directories: include_directories('.'),
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_object_ring=new_object_ring',
    '-Wl,--export-dynamic-symbol=new_object_ring',
  ],
)
