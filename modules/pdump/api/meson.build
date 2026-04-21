pdump_dataplane_inc = include_directories('../dataplane')

# libpcap: git submodule at subprojects/libpcap, built with CMake into this
# module's build dir (Meson's cmake.subproject hits a dependency cycle in upstream libpcap).
fs = import('fs')
cmake = find_program('cmake', required: true, version: '>=3.5')
pcap_src_root = meson.project_source_root() / 'subprojects' / 'libpcap'
if not fs.exists(pcap_src_root / 'CMakeLists.txt')
  error(
    'pdump: missing libpcap sources at subprojects/libpcap. Initialize git submodules: git submodule update --init --recursive',
  )
endif

pcap_cmake_build = meson.current_build_dir() / 'external' / 'libpcap-cmake'
pcap_prefix = meson.current_build_dir() / 'external' / 'libpcap-install'
libpcap_a = pcap_prefix / 'lib' / 'libpcap.a'

pcap_cmake_args = [
  '-S', pcap_src_root,
  '-B', pcap_cmake_build,
  '-G', 'Ninja',
  '-DCMAKE_INSTALL_PREFIX=' + pcap_prefix,
  '-DCMAKE_BUILD_TYPE=' + (get_option('buildtype') == 'debug' ? 'Debug' : 'RelWithDebInfo'),
  '-DBUILD_SHARED_LIBS=OFF',
  '-DDISABLE_BLUETOOTH=ON',
  '-DDISABLE_NETMAP=ON',
  '-DDISABLE_DPDK=ON',
  '-DDISABLE_DBUS=ON',
  '-DDISABLE_RDMA=ON',
  '-DDISABLE_DAG=ON',
  '-DDISABLE_SEPTEL=ON',
  '-DDISABLE_SNF=ON',
  '-DDISABLE_TC=ON',
  '-DCMAKE_C_FLAGS=' + ' '.join(yanet_c_args),
  '-DCMAKE_EXE_LINKER_FLAGS=' + ' '.join(yanet_link_args),
  '-DCMAKE_SHARED_LINKER_FLAGS=' + ' '.join(yanet_link_args),
  '-DCMAKE_MODULE_LINKER_FLAGS=' + ' '.join(yanet_link_args),
]
if host_machine.system() == 'linux'
  pcap_cmake_args += ['-DBUILD_WITH_LIBNL=OFF', '-DDISABLE_LINUX_USBMON=ON']
endif

if not fs.exists(libpcap_a)
  message('[pdump] Configuring libpcap with CMake in ' + pcap_cmake_build)
  run_command(cmake, pcap_cmake_args, check: true)
  message('[pdump] Building and installing libpcap to ' + pcap_prefix)
  run_command(cmake, '--build', pcap_cmake_build, '--target', 'install', check: true)
else
  message('[pdump] Reusing built libpcap: ' + libpcap_a)
endif

pcap_libdir = pcap_prefix / 'lib'
pcap_incdir = pcap_prefix / 'include'
message('[pdump] Linking pdump_cp with static libpcap from ' + pcap_libdir)
libpcap_dep = declare_dependency(
  compile_args: ['-I' + pcap_incdir],
  dependencies: cc.find_library('pcap', dirs: pcap_libdir, static: true),
)

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
