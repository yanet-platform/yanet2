subdir('time')
subdir('packet')
subdir('module')
subdir('pipeline')

# The generation zone: the dataplane side of config publication that the
# controlplane libraries call into, kept apart from the packet hot path.
#
# Its sources are in config/, but the controlplane config library needs it
# before lib/meson.build enters that directory, whose dataplane config
# library in turn needs the controlplane one.
lib_dp_zone = static_library(
  'dp_zone',
  files('config/gen_resolve.c', 'config/zone.c'),
  dependencies: [lib_logging_dep],
  install: false,
)

lib_dp_zone_dep = declare_dependency(
  link_with: [lib_dp_zone],
  include_directories: yanet_rootdir,
  dependencies: [lib_logging_dep],
)

subdir('worker')
