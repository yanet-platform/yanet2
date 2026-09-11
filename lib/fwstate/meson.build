# FWState library - firewall state tracking components
dependencies = [
    lib_common_dep,
    lib_packet_dp_dep,
    libdpdk_dep,
    lib_statemap_dep,
]

sources = files(
  'sync.c',
  'lookup.c',
)

lib_fwstate = static_library(
  'fwstate',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

# The registry translation unit holds only a constructor registering the
# domain callbacks; link_whole on the dependency forces it into every binary
# consuming the library, so the registration always runs.
lib_fwstate_registry = static_library(
  'fwstate_registry',
  'registry.c',
  c_args: yanet_c_args,
  # Headers only: whole-archive propagation must not drag the dataplane
  # dependency closure into consumers.
  dependencies: [lib_statemap_dep],
  install: false,
)

lib_fwstate_dep = declare_dependency(
  link_with: lib_fwstate,
  link_whole: [lib_fwstate_registry],
  include_directories: yanet_rootdir,
  dependencies: dependencies,
)
