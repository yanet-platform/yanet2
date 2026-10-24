dp_dependencies = [
  lib_common_dep,
  lib_fwstate_dep,
  lib_fwstate_objects_dep,
  lib_classify_query_dep,
  lib_packet_dp_dep,
  lib_module_dp_dep,
  lib_worker_dp_dep,
]

dp_sources = files(
  'dataplane.c',
)

lib_acl_dp = static_library(
  'acl_dp',
  dp_sources,
  dependencies: dp_dependencies,
  install: false,
)

lib_acl_dp_dep = declare_dependency(
  link_with: lib_acl_dp,
  link_args: [
    '-Wl,--require-defined=new_module_acl',
    '-Wl,--export-dynamic-symbol=new_module_acl',
  ],
)
