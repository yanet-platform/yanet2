ring_object_test = executable(
  'ring_object_test',
  ['object_test.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_dataplane_ut_dep,
    lib_logging_dep,
    lib_errors_dep,
    lib_ring_objects_dep,
    libdpdk_dep,
  ],
  include_directories: yanet_rootdir,
)

test('ring_object', ring_object_test, suite: 'ring')

# C writer helpers for the Go ringtest package. Meson builds them with the
# same cache line size as the ring object archive.
static_library(
  'ringtest_writer',
  ['ringtest_writer.c'],
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [
    lib_common_dep,
    lib_config_cp_dep,
    lib_errors_dep,
    libdpdk_inc_dep,
  ],
  include_directories: yanet_rootdir,
  install: false,
)
