# FWState library - firewall state tracking components
dependencies = [
    lib_common_dep,
    libdpdk_inc_dep,
    lib_statemap_dep,
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

# The registry of fwstate's fwmap callbacks, which a binary that uses fwstate
# maps links whole so that its constructor runs.
#
# Table creators also call its exported ensure function, for processes that
# reach the tables without running the constructor.
lib_fwstate_registry = static_library(
  'fwstate_registry',
  'registry.c',
  dependencies: [lib_statemap_dep],
  install: false,
)

# The registry alone, for code that uses fwstate maps without the rest of the
# library.
lib_fwstate_registry_dep = declare_dependency(
  link_whole: [lib_fwstate_registry],
  dependencies: [lib_statemap_dep],
)

lib_fwstate_dep = declare_dependency(
  link_with: lib_fwstate,
  include_directories: yanet_rootdir,
  dependencies: dependencies + [lib_fwstate_registry_dep],
)
