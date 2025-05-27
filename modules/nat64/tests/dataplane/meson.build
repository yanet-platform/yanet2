dependencies = [
  libdpdk_dep,
  lib_common_dep,
  lib_module_dp_dep,
  lib_config_dp_dep,
  lib_logging_dep,
]


sources = files(
  '../../api/nat64cp.c',
  '../../dataplane/nat64dp.c',
  'commands.c',
  'main.c',
  'nat64_test.c',
)

u = executable(
  'unittest',
  sources,
  include_directories: [yanet_rootdir, '.', '../..', '../../..', '../../dataplane'],
  dependencies: dependencies,
)

test('nat64 unittest', u, env: ['YANET_TEST=nat64_autotest'])

subdir('go')
