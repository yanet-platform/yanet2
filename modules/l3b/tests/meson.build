subdir('dataplane')

static_library(
  'l3b_probe_service',
  files('internal/probe/native/service.c'),
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: [libdpdk_inc_dep],
  include_directories: [
    yanet_rootdir,
    include_directories('internal/probe'),
  ],
  install: false,
)
