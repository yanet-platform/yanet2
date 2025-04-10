dependencies = []

dependencies += libdpdk_dep

dependencies += lib_common_dep
dependencies += lib_module_dp_dep
dependencies += lib_config_dp_dep
dependencies += lib_logging_dep


sources = files(
  'main.c',
  '../nat64dp.c',
  '../nat64cp.c',
  'nat64_test.c',
  'commands.c'
)


u = executable(
  'unittest',
  sources,
  include_directories: [yanet_rootdir, '.', '..'],
  dependencies: dependencies,
  c_args : '-DDEBUG_NAT64'
)

test('nat64 unittest', u,
env: ['YANET_TEST=nat64_autotest'])
