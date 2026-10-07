# YANET module SDK: the build-side surface for out-of-tree dataplane module
# plugins (.so).
#
# The runtime half already lives in lib/dataplane/config: the dataplane
# scans plugin_dir for lib<name>_dp.so files, refuses any whose exported
# yanet_module_abi_version does not match, and dp_load_module prefers a
# plugin's new_module_<name> over the statically linked built-ins.
#
# This directory supplies the build half for a module developed outside the
# yanet2 meson project:
#
#   - libmodule_abi_export.a, a PIC archive stamping a plugin with the
#     compiled-against YANET_MODULE_ABI_VERSION, and
#   - build/sdk/yanet-module-sdk.pc, carrying the include paths and defines
#     an in-tree module translation unit compiles with.
#
# An out-of-tree module consumes both by pointing PKG_CONFIG_PATH at
# <builddir>/sdk and depending on yanet-module-sdk; see docs/module-sdk.md
# and sdk/example for a complete reference module.

libmodule_abi_export = static_library(
  'module_abi_export',
  c_args: yanet_c_args,
  # Carries plugin_abi_export.c itself plus the tree-rooted include path.
  dependencies: [lib_module_abi_export_dep],
  # Linked exclusively into module .so targets, so position independence is
  # the point. The archive must never reach the statically linked
  # built-ins (see lib/dataplane/config/meson.build).
  pic: true,
  install: false,
)

# Mirror the compile environment an in-tree module translation unit gets:
# the yanet source root (headers are tree-rooted), the build root
# (generated yanet_build_config.h and rte_build_config.h), and DPDK's
# config and per-library include directories. DPDK driver headers are
# deliberately left out: module code never includes them, and the driver
# set varies with the enabled PMDs.
find_tool = find_program('find')
dpdk_lib_dirs = run_command(
  find_tool,
  join_paths(meson.project_source_root(), 'subprojects', 'dpdk', 'lib'),
  '-mindepth', '1',
  '-maxdepth', '2',
  '-type', 'd',
  check: true,
).stdout().strip().split('\n')

sdk_inc_dirs = [
  meson.project_source_root(),
  meson.global_build_root(),
  join_paths(meson.project_source_root(), 'subprojects', 'dpdk'),
  join_paths(meson.global_build_root(), 'subprojects', 'dpdk'),
  join_paths(meson.project_source_root(), 'subprojects', 'dpdk', 'config'),
] + dpdk_lib_dirs
sdk_inc_flags = '-I' + ' -I'.join(sdk_inc_dirs)

# The same cache-line define the Makefile derives for `go test`: module and
# dataplane must agree on it for the alignment-sensitive shared structs.
awk_tool = find_program('awk')
cache_line_size = run_command(
  awk_tool,
  '$2 == "RTE_CACHE_LINE_SIZE" { print $3; exit }',
  join_paths(meson.global_build_root(), 'subprojects', 'dpdk', 'rte_build_config.h'),
  check: true,
).stdout().strip()

sdk_defines = [
  '-D_GNU_SOURCE',
  '-DYANET_CACHE_LINE_SIZE=' + cache_line_size,
]
if yanet_isa_flag != ''
  sdk_defines += [yanet_isa_flag]
endif

sdk_conf = configuration_data()
sdk_conf.set('YANET_VERSION', yanet_version)
sdk_conf.set('YANET_SRCROOT', meson.project_source_root())
sdk_conf.set('YANET_BUILDDIR', meson.global_build_root())
sdk_conf.set('SDK_INC_FLAGS', sdk_inc_flags)
sdk_conf.set('SDK_DEFINES', ' '.join(sdk_defines))

configure_file(
  input: 'yanet-module-sdk.pc.in',
  output: 'yanet-module-sdk.pc',
  configuration: sdk_conf,
)
