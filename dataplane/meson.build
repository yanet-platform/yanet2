lib_dependencies = [
  dependency('threads'),
  dependency('yaml-0.1'),
  # Headers only: the full DPDK dependency would copy every DPDK object
  # into libdataplane.a; the executable below links it instead.
  libdpdk_inc_dep,

  lib_common_dep,
  lib_logging_dep,
  lib_time_dp_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_worker_dp_dep,
  lib_counters_dep,
  lib_dp_zone_dep,
  lib_packet_dp_dep,
  lib_errors_dep,
]

lib_sources = files(
  'config.c',
  'dataplane.c',
  'device.c',
  'dpdk.c',
  'flow.c',
  'instance.c',
  'worker.c',
  'numa.c',
)

lib_dataplane = static_library(
  'dataplane',
  lib_sources,
  dependencies: lib_dependencies,
  install: false,
)

lib_dataplane_dep = declare_dependency(
  link_with: lib_dataplane,
  include_directories: include_directories('.'),
  dependencies: lib_dependencies,
)

# Startup finds each built-in module, object and device by its constructor's
# name; each appended dependency links that constructor in and exports it.
dataplane_exe_deps = [
  lib_common_dep,
  lib_logging_dep,
  libdpdk_dep,
  lib_dataplane_dep,
] + dataplane_module_deps + dataplane_object_deps + dataplane_device_deps

executable(
  'yanet-dataplane',
  files('main.c'),
  dependencies: dataplane_exe_deps,
  install: true,
  # Plugins loaded at run time resolve the dataplane symbols they call against
  # this executable.
  export_dynamic: true,
)

subdir('unittest')
