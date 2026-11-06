# The build-side half of the module SDK: the archive stamping a plugin with
# the ABI version it was compiled against, and the pkg-config metadata an
# out-of-tree module compiles and links with.
#
# The loader half (plugin discovery and the ABI check) lives in the
# dataplane; docs/module-sdk.md describes the contract and sdk/example is
# the reference consumer.

# Not captured in a variable on purpose: nothing in this build consumes
# the target object, the .pc names the archive by its output path, and an
# unused assignment trips the meson linter.
static_library(
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
# per-library include directories on both the source side and the build
# side. EAL carries OS- and arch-specific include dirs (rte_os.h lives in
# lib/eal/<os>/include and pulls the libc headers yanet's spinlock relies
# on), so those are added for the host pair only, exactly as DPDK's own
# meson does — globbing every OS dir would let freebsd/rte_os.h win.
# DPDK driver headers are deliberately left out: module code never
# includes them, and the driver set varies with the enabled PMDs.
find_tool = find_program('find')
dpdk_libroot = join_paths(meson.project_source_root(), 'subprojects', 'dpdk', 'lib')
dpdk_build_libroot = join_paths(meson.global_build_root(), 'subprojects', 'dpdk', 'lib')

# EAL's arch include dir name per DPDK's own layout.
dpdk_cpu_map = {
  'x86_64': 'x86',
  'aarch64': 'arm',
  'loongarch64': 'loongarch',
  'ppc64': 'ppc',
  'riscv64': 'riscv',
}
dpdk_cpu = dpdk_cpu_map.get(host_machine.cpu_family(), '')
eal_inc_dirs = ['include', 'common', host_machine.system() / 'include']
if dpdk_cpu != ''
  eal_inc_dirs += [dpdk_cpu / 'include']
endif

dpdk_lib_dirs = []
foreach dir : run_command(
  find_tool,
  [dpdk_libroot, dpdk_build_libroot],
  '-mindepth', '1',
  '-maxdepth', '1',
  '-type', 'd',
  # A reconfigure of an already-built tree would otherwise pick up the
  # per-target object directories the build created since the setup.
  '-not', '-name', '*.p',
  check: true,
).stdout().strip().split('\n')
  dpdk_lib_dirs += [dir]
  if dir.endswith('lib/eal')
    foreach eal : eal_inc_dirs
      dpdk_lib_dirs += [dir / eal]
    endforeach
  endif
endforeach

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

# A sanitized tree instruments the ABI archive like every other target, so
# a plugin linking it wholesale must compile and link with the same
# sanitizers or its objects stay unresolved; the set mirrors the supported
# sanitized builds (ASan+UBSan and TSan).
sdk_sanitizers = []
foreach sanitizer : ['address', 'undefined', 'thread']
  if b_sanitize.contains(sanitizer)
    sdk_sanitizers += [sanitizer]
  endif
endforeach
sdk_sanitize_flags = ''
if sdk_sanitizers.length() > 0
  sdk_sanitize_flags = '-fsanitize=' + ','.join(sdk_sanitizers)
endif

sdk_cflags = [sdk_inc_flags, ' '.join(sdk_defines)]
sdk_libs = []
if sdk_sanitize_flags != ''
  sdk_cflags += [sdk_sanitize_flags]
  sdk_libs += [sdk_sanitize_flags]
endif
# rte_config.h first (the DPDK headers need it), then the ABI tripwires:
# every plugin translation unit compiles them with the plugin's own
# compiler and flags, not just the prebuilt archive.
sdk_cflags += [
  '-include rte_config.h',
  '-include lib/dataplane/config/plugin_abi_assert.h',
]

# Nothing references the ABI version symbol at link time (the loader
# dlsym's it), so the archive is linked wholesale or the linker would drop
# its only object.
sdk_libs += [
  '-L${yanet_builddir}/sdk',
  '-Wl,--whole-archive',
  '-lmodule_abi_export',
  '-Wl,--no-whole-archive',
]

# Read straight from the header both sides compile, so the .pc can never
# drift from the loader's expectation (consumers assert their mirrors
# against this variable).
module_abi_version = cc.get_define(
  'YANET_MODULE_ABI_VERSION',
  prefix: '#include "lib/dataplane/module/module.h"',
  args: ['-I' + meson.project_source_root()],
).strip()

sdk_conf = configuration_data()
sdk_conf.set('YANET_VERSION', yanet_version)
sdk_conf.set('YANET_SRCROOT', meson.project_source_root())
sdk_conf.set('YANET_BUILDDIR', meson.global_build_root())
sdk_conf.set('YANET_MODULE_ABI_VERSION', module_abi_version)
sdk_conf.set('SDK_SANITIZE_FLAGS', sdk_sanitize_flags)
sdk_conf.set('SDK_CFLAGS', ' '.join(sdk_cflags))
sdk_conf.set('SDK_LIBS', ' '.join(sdk_libs))

configure_file(
  input: 'yanet-module-sdk.pc.in',
  output: 'yanet-module-sdk.pc',
  configuration: sdk_conf,
)
