cp_dependencies = [
  lib_common_dep,
  lib_errors_dep,
  lib_classify_compiler_dep,
  lib_classify_query_dep,
  lib_config_cp_dep,
  lib_counters_dep,
  lib_packet_dp_dep,
]

includes = include_directories('../../..')

sources = files(
  'classifier_object.c',
)

lib_classifier_objects = static_library(
  'classifier_objects',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: includes,
  install: false,
)

lib_classifier_objects_dep = declare_dependency(
  link_with: [lib_classifier_objects],
  include_directories: include_directories('.'),
  link_args: [
    '-Wl,--defsym',
    '-Wl,new_object_classifier=new_object_classifier',
    '-Wl,--export-dynamic-symbol=new_object_classifier',
  ],
)
