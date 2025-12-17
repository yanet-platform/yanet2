# FWState library - firewall state tracking components
dependencies = [
    lib_common_dep,
    lib_packet_dp_dep,
    libdpdk_dep,
]

sources = files(
  'sync.c',
  'lookup.c',
)

lib_fwstate = static_library(
  'fwstate',
  sources,
  dependencies: dependencies,
  install: false,
)

lib_fwstate_dep = declare_dependency(
  link_with: lib_fwstate,
  include_directories: yanet_libdir,
  dependencies: dependencies,
)
