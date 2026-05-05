pdump_dataplane_inc = include_directories('../dataplane')

# Static libpcap from subprojects/libpcap submodule (built via Meson's CMake module).
cmake = import('cmake')
pcap_opts = cmake.subproject_options()
pcap_opts.add_cmake_defines({
  'BUILD_SHARED_LIBS': 'OFF',
  'DISABLE_BLUETOOTH': 'ON',
  'DISABLE_NETMAP': 'ON',
  'DISABLE_DPDK': 'ON',
  'DISABLE_DBUS': 'ON',
  'DISABLE_RDMA': 'ON',
  'DISABLE_DAG': 'ON',
  'DISABLE_SEPTEL': 'ON',
  'DISABLE_SNF': 'ON',
  'DISABLE_TC': 'ON',
  'BUILD_WITH_LIBNL': 'OFF',
  'DISABLE_LINUX_USBMON': 'ON',
  'CMAKE_MESSAGE_LOG_LEVEL': 'WARNING',
  'CMAKE_SUPPRESS_DEVELOPER_WARNINGS': 'ON',
})
# Don't propagate -Werror/-Wall/-Wextra into the third-party subproject;
# also drop the project-wide -D_GNU_SOURCE because libpcap defines it itself
# in ftmacros.h, which would otherwise trigger -Wmacro-redefined.
pcap_opts.set_override_option('werror', 'false')
pcap_opts.set_override_option('warning_level', '0')
pcap_opts.append_compile_args('c', '-U_GNU_SOURCE')
libpcap_dep = cmake.subproject('libpcap', options: pcap_opts).dependency('pcap_static')

cp_dependencies = [lib_common_dep, lib_errors_dep, lib_config_cp_dep, libdpdk_dep, libpcap_dep]
api_sources = files('controlplane.c')

lib_pdump_cp = static_library(
  'pdump_cp',
  api_sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: cp_dependencies,
  include_directories: [yanet_rootdir, pdump_dataplane_inc],
  install: false,
)
lib_pdump_cp_dep = declare_dependency(
  link_with: lib_pdump_cp,
  dependencies: cp_dependencies,
)
