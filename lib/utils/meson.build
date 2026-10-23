dependencies = [
    lib_packet_dp_dep,
    libdpdk_inc_dep
]

sources = files(
  'packet.c',
)

lib_lib_utils = static_library(
  'lib_utils',
  sources,
  include_directories: [yanet_rootdir],
  install: false,
  dependencies: dependencies,
)

lib_lib_utils_dep = declare_dependency(
  link_with: lib_lib_utils,
  include_directories: yanet_rootdir,
  dependencies: dependencies
)

test_utils = executable(
  'test_utils',
  sources: 'test.c',
  dependencies: lib_lib_utils_dep,
  link_with: [lib_lib_utils, lib_packet_dp]
)
test('test_utils', test_utils, suite: 'lib_utils')
