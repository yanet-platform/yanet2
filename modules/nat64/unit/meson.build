dependencies = []

dependencies += libdpdk_dep
dependencies += lib_filter_dep

dependencies += lib_common_dep
dependencies += lib_module_dp_dep


sources = files(
  'main.c',
  '../dataplane.c',
  'test.c',
  'commands.c'
)

u = executable(
  'unittest',
  sources,
  include_directories: [yanet_rootdir, '.', '..'],
  dependencies: dependencies
)

test('nat64 unittest', u,
env: ['DPDK_TEST=nat64_autotest'])
