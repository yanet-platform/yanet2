cp_dependencies = [
  lib_common_dep,
  lib_config_cp_dep,
]

ext_deps = ['rte_bpf']

foreach e:ext_deps
  cp_dependencies += libdpdk.get_variable('static_' + e)
endforeach

pdump_cp_link_deps = []
foreach l:libdpdk.get_variable('dpdk_static_libraries')
  if l.name() in ext_deps 
    pdump_cp_link_deps += l
  endif
endforeach

pdump_dataplane_inc = include_directories('../dataplane')

api_sources = files(
  'controlplane.c',
)

lib_pdump_cp = static_library(
  'pdump_cp',
  api_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  link_whole: pdump_cp_link_deps,
  dependencies: cp_dependencies,
  include_directories: [yanet_rootdir, pdump_dataplane_inc],
  install: false,
)

lib_pdump_cp_dep = declare_dependency(
  link_with: lib_pdump_cp,
)
