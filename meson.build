project(
  'yanet',
  'c',
  meson_version: '>= 1.3',
  version: '0.3.0',
  default_options: [
    'buildtype=release',
    'warning_level=2',
    'werror=true',
    'optimization=2',
    'prefix=/usr',
    'libdir=lib',
    'bindir=bin',
  ],
)

yanet_project_args = ['-g', '-D_GNU_SOURCE']
# Also held on its own because the dataplane version banner reports it.
yanet_isa_flag = ''
if host_machine.cpu_family() == 'x86_64'
  yanet_isa_flag = '-march=' + get_option('cpu_arch')
  yanet_project_args += yanet_isa_flag
endif
# The aarch64 branch appends its own ISA flag to yanet_project_args
# further down, once the DPDK subproject() has resolved its ISA baseline.

# Stable config: changes only when a build option changes, so it stays a
# widely-included header without dragging per-build values into every
# translation unit's ccache manifest.
yanet_conf = configuration_data()
yanet_conf.set('DEBUG_NAT64', get_option('buildtype') == 'debug')
yanet_conf.set('ENABLE_TRACE_LOG', get_option('trace').enabled())
yanet_conf.set('MBUF_MAX_SIZE', get_option('mbuf_max_size'))

build_cfg = 'yanet_build_config.h'
configure_file(output: build_cfg, configuration: yanet_conf)

# Volatile config: the commit and the timestamp, read when meson configures the
# tree and re-read on every reconfigure.
#
# Kept out of yanet_build_config.h and included only by the version banner, so
# it does not poison ccache manifests across the rest of the tree. The header
# itself is written further down, once the DPDK subproject has resolved the
# target ISA and the sanitizer set is final.
yanet_stamp = configuration_data()

# Version and build info
yanet_version = get_option('version') == '' ? meson.project_version() : get_option('version')

cc = meson.get_compiler('c')
yanet_stamp.set_quoted('YANET_VERSION', yanet_version)
yanet_stamp.set_quoted('YANET_COMPILER_ID', cc.get_id())
yanet_stamp.set_quoted('YANET_COMPILER_VERSION', cc.version())
yanet_stamp.set_quoted('YANET_BUILD_TYPE', get_option('buildtype'))

# Git commit (if available)
git = find_program('git', required: false)
if git.found()
    git_commit = run_command(git, 'rev-parse', '--short', 'HEAD', check: false)
    if git_commit.returncode() == 0
        yanet_stamp.set_quoted('YANET_GIT_COMMIT', git_commit.stdout().strip())
    else
        yanet_stamp.set_quoted('YANET_GIT_COMMIT', 'unknown')
    endif
else
    yanet_stamp.set_quoted('YANET_GIT_COMMIT', 'unknown')
endif

# Build date. Honours SOURCE_DATE_EPOCH (set by debhelper) for reproducible
# builds, falling back to the wall clock otherwise.
build_date = run_command(
    'sh', '-c',
    'if [ -n "$SOURCE_DATE_EPOCH" ]; then ' +
    'date -u -d "@$SOURCE_DATE_EPOCH" +"%Y-%m-%d %H:%M:%S"; ' +
    'else date -u +"%Y-%m-%d %H:%M:%S"; fi',
    check: true,
)
yanet_stamp.set_quoted('YANET_BUILD_DATE', build_date.stdout().strip())

dataplane_only = get_option('dataplane_only')
if dataplane_only and get_option('sdk_rust_fixtures').enabled()
  error('sdk_rust_fixtures needs the control-plane build, not dataplane_only')
endif

python3 = find_program('python3', required: true, version: '>=3.8', native: true)

if not dataplane_only
  # Define common programs once to avoid duplication
  go = find_program('go', required: true)
  ar = find_program('ar', required: true)
  go_cdeps = [
    python3,
    files('scripts/go_cdeps.py'),
    '--root', meson.project_source_root(),
    '--build-root', meson.project_build_root(),
    '--go', go.full_path(),
    '--ar', ar.full_path(),
  ]
  test('go-cdeps', python3, args: files('scripts/test_go_cdeps.py'), timeout: 120)
  protoc = find_program('protoc', required: true)
  find_program('protoc-gen-go', required: true, version: '>=1.36.5')
  find_program('protoc-gen-go-grpc', required: true, version: '>=1.5.1')

  # protoc as make proto-go runs it: every proto is named by its path from the
  # repository root and gets its Go code next to it.
  #
  # A target appends its inputs, and a depfile when it compiles a single proto:
  # protoc writes a depfile only for a single input. The protoc Ubuntu 22.04
  # ships still wants an explicit opt-in for proto3 optional fields.
  go_protoc = [
    protoc,
    '--experimental_allow_proto3_optional',
    '-I', meson.project_source_root(),
    '--go_out=paths=source_relative:' + meson.project_source_root(),
    '--go-grpc_out=paths=source_relative:' + meson.project_source_root(),
    '--descriptor_set_out=@OUTPUT@',
  ]
endif

# Build CGO flags based on b_sanitize option
yanet_go_env = {}
sanitize_link_args = []
b_sanitize = get_option('b_sanitize')
# Every sanitizer the C tree is instrumented with, as the version banner
# reports it. The fuzzing build below adds the ones it forces on.
yanet_sanitizers = []
foreach sanitizer : ['address', 'undefined', 'thread', 'memory', 'leak']
  if b_sanitize.contains(sanitizer)
    yanet_sanitizers += sanitizer
  endif
endforeach
if b_sanitize != 'none'
    yanet_project_args += '-DYANET_SANITIZE'
    sanitize_flags = []
    if b_sanitize.contains('address')
        sanitize_flags += 'address'
    endif
    if b_sanitize.contains('undefined')
        sanitize_flags += 'undefined'
    endif
    if sanitize_flags.length() > 0
        sanitize_str = ','.join(sanitize_flags)
        cgo_flags = '-fsanitize=' + sanitize_str + ' -g'
        # gcc links the shared sanitizer runtimes without adding a runpath;
        # $ORIGIN lets a binary load copies staged beside it wherever it is
        # deployed.
        yanet_go_env = {
            'CGO_CFLAGS': cgo_flags,
            'CGO_LDFLAGS': cgo_flags + ' -Wl,-rpath,$ORIGIN',
        }
        sanitize_link_args = ['-Wl,-rpath,$ORIGIN']
    endif
endif

# Tests check their results with assert(), so no build may compile it out.
yanet_test_c_args = ['-O0', '-UNDEBUG']
yanet_c_args = []
yanet_link_args = sanitize_link_args
if get_option('fuzzing').enabled()
  fuzz_args = [
    '-fsanitize=fuzzer-no-link,address,undefined',
    '-fsanitize-coverage=edge,indirect-calls,trace-cmp',
    '-fprofile-instr-generate',
    '-fcoverage-mapping',
  ]

  # For fuzzing builds, all libraries should be instrumented with coverage counters.
  yanet_c_args += fuzz_args
  yanet_link_args += fuzz_args
  foreach sanitizer : ['fuzzer', 'address', 'undefined']
    if not yanet_sanitizers.contains(sanitizer)
      yanet_sanitizers += sanitizer
    endif
  endforeach

  # The cgo flags of a fuzzing build: the cgo dependency markers scan the cgo
  # sources with them.
  cgo_flags = '-fsanitize=address -g'
  yanet_go_env = {
      'CGO_CFLAGS': cgo_flags,
      'CGO_LDFLAGS': cgo_flags,
  }
endif

yanet_rootdir = include_directories('.')

# Initialize fuzzing targets dictionary
# Modules will populate this with their fuzzing targets
fuzzing_targets = {}

# The modules, objects and devices built into the dataplane executable: each
# one appends the dependency that links and registers it.
dataplane_module_deps = []
dataplane_object_deps = []
dataplane_device_deps = []

yanet_dpdk_platform = get_option('dpdk_platform')
libdpdk_default_options = [
  'platform=' + yanet_dpdk_platform,
  'pkt_mbuf_headroom=@0@'.format(get_option('pkt_mbuf_headroom')),
  # An empty enable_apps means "build every app", so any app a disable list
  # fails to name is still built. The glob also covers apps added upstream
  # later, and nothing outside subprojects/ uses any of them.
  'disable_apps=*',
  'enable_apps=',
  # On top of DPDK's always-enabled core: timer and bpf for the dataplane,
  # security and cryptodev for net/iavf. DPDK refuses to configure when a
  # listed driver lacks a library; our own code fails to compile instead.
  'enable_libs=bpf,timer,security,cryptodev',
  'enable_driver_sdk=true',
  'enable_drivers=net/mlx5,common/mlx5,bus/auxiliary,net/virtio,net/e1000,net/iavf,common/iavf,net/i40e,net/ring',
  'default_library=static',
  'tests=false',
  'examples=',
  'werror=false',
  # DPDK turns developer mode on whenever its tree holds .git, which a
  # submodule always does; its symbol checks guard DPDK's own shared ABI.
  'developer_mode=disabled',
  'c_args=-Wno-stringop-overflow -Wno-array-bounds -Wno-stringop-overread -Wno-vla-larger-than',
]
if host_machine.cpu_family() == 'x86_64'
  libdpdk_default_options += 'cpu_instruction_set=' + get_option('cpu_arch')
endif

# clang's undefined behaviour checks skip the DPDK spots the list names.
#
# They report code there that gcc's checks pass over. Only global arguments
# reach the sources of a subproject, and a subproject may not set them itself.
# ccache hashes the arguments but not the list they name, so the list is
# copied under a name that carries its content hash: an edited list changes
# every compile command and misses the cache.
if (cc.get_id() == 'clang' and b_sanitize.contains('undefined')
    and not meson.is_subproject())
  ubsan_ignorelist = 'ubsan-ignorelist-@0@.txt'.format(
    import('fs').hash('ubsan-ignorelist.txt', 'md5'),
  )
  configure_file(
    input: 'ubsan-ignorelist.txt',
    output: ubsan_ignorelist,
    copy: true,
  )
  add_global_arguments(
    '-fsanitize-ignorelist=' + meson.current_build_dir() / ubsan_ignorelist,
    language: 'c',
  )
endif

libdpdk = subproject(
  'dpdk',
  default_options: libdpdk_default_options,
)

libdpdk_dep = libdpdk.get_variable('dpdk_dep')

# DPDK for test programs: every library, but of the drivers only the buses
# and mempools.
#
# No test probes a NIC, and linking every NIC driver into each test program
# is most of what it costs to link one.
dpdk_test_drivers = []
foreach driver : libdpdk.get_variable('dpdk_drivers')
  if driver.name().startswith('rte_bus_') or driver.name().startswith('rte_mempool_')
    dpdk_test_drivers += driver
  endif
endforeach
libdpdk_test_dep = declare_dependency(
  dependencies: libdpdk_dep.partial_dependency(compile_args: true, includes: true, link_args: true),
  link_whole: libdpdk.get_variable('dpdk_static_libraries') + dpdk_test_drivers,
)

yanet_cache_line_size = libdpdk.get_variable('dpdk_conf').get('RTE_CACHE_LINE_SIZE')
yanet_cache_line_arg = '-DYANET_CACHE_LINE_SIZE=' + yanet_cache_line_size.to_string()
yanet_project_args += yanet_cache_line_arg
yanet_go_env += {
  'CGO_CPPFLAGS': yanet_cache_line_arg,
}

# cgo in meson's Go builds compiles and links with meson's C compiler.
#
# A sanitized build then links the sanitizer runtime of the compiler that
# instrumented its libraries. A plain cc or gcc is left to Go's default, gcc,
# so its cgo packages share the build cache with Go runs outside meson. Go runs
# the first word of CC alone to probe linker flags and to key that cache, so CC
# names the compiler without the compiler cache meson may put first.
yanet_go_cc = []
foreach word : cc.cmd_array()
  if (yanet_go_cc.length() > 0
      or import('fs').name(word) not in ['ccache', 'sccache'])
    yanet_go_cc += word
  endif
endforeach
if not (cc.get_id() == 'gcc' and yanet_go_cc.length() == 1
        and import('fs').name(yanet_go_cc[0]) in ['cc', 'gcc'])
  yanet_go_env += {'CC': ' '.join(yanet_go_cc)}
endif

if get_option('sdk_rust_fixtures').enabled()
  yanet_go_env += {
    'CGO_CPPFLAGS': yanet_go_env['CGO_CPPFLAGS']
      + ' "-I' + meson.project_source_root() + '"'
      + ' "-I' + (meson.project_build_root() / 'lib/rust') + '"'
      + ' "-I' + (meson.global_build_root() / 'subprojects/dpdk') + '"',
  }
endif

# The same cgo preprocessor flags for Go builds outside meson, which the
# Makefile, the gate and direct go test runs read from this file.
configure_file(
  output: 'yanet_cgo_cppflags',
  command: ['echo', yanet_go_env['CGO_CPPFLAGS']],
  capture: true,
)

# gRPC-only operators never touch shared memory and link no cgo.
#
# Building them with cgo disabled turns a stray import of a module control
# plane or of the FFI bindings into a loud build failure; with cgo on it
# would silently restore the cgo link, which meson cannot see and which
# then fails only as an ordering-dependent "cannot find -l<lib>" error.
# Because it snapshots the Go build environment above, it must stay
# below the last assignment to that environment.
yanet_go_nocgo_env = yanet_go_env + {'CGO_ENABLED': '0'}

# The Go binaries are not fuzz targets and the fuzz suite runs none of them,
# so a fuzzing build skips them to save build time.
yanet_go_binaries = not get_option('fuzzing').enabled()

# Taking only the include directories strips the compile args carried
# by dpdk_dep, including -include rte_config.h, so it has to be
# re-added here. DPDK's machine args are deliberately left out.
libdpdk_inc_dep = declare_dependency(
  dependencies: libdpdk_dep.partial_dependency(includes: true),
  compile_args: ['-include', 'rte_config.h'],
)

if host_machine.cpu_family() == 'aarch64'
  # The yanet -march/-mcpu flag has to match what DPDK resolves for
  # dpdk_dep, or two conflicting -march/-mcpu switches land on the same
  # compile command line.
  #
  # DPDK derives its own ISA baseline from dpdk_platform (or, in a cross
  # build, from the cross file's platform property, which overrides the
  # option) and carries it through dpdk_dep's compile args into every
  # target that depends on it. The table below mirrors
  # subprojects/dpdk/config/arm/meson.build's part_number_config tables for
  # the same platform names. Ampere Altra omits +crc: CRC is mandatory from
  # armv8.1-a onward and neoverse-n1 (armv8.2-a) implies it, so
  # common/crc32.h's __crc32c* intrinsics still work without asking for it
  # explicitly.
  #
  # The table lists only these platforms on purpose: a platform whose DPDK
  # config has no mcpu switch would otherwise configure and compile
  # silently with yanet and DPDK at different ISA baselines, so an
  # unlisted platform is rejected here rather than guessed.
  #
  # The native platform is exempt from the table and the assert below:
  # setting the platform to native makes DPDK probe the local CPU's
  # implementer and part number at configure time, so there is no fixed
  # flag to tabulate or cross-check, and the resolved flag is adopted
  # directly instead. In a cross build, DPDK takes the SoC from the cross
  # file's platform property and requires it to name a supported SoC —
  # so DPDK itself errors out before this code runs.
  if meson.is_cross_build()
    aarch64_platform = meson.get_external_property('platform', get_option('dpdk_platform'))
  else
    aarch64_platform = get_option('dpdk_platform')
  endif
  yanet_dpdk_platform = aarch64_platform

  dpdk_machine_args = libdpdk.get_variable('machine_args')
  dpdk_aarch64_isa_flag = dpdk_machine_args.length() > 0 ? dpdk_machine_args[0] : ''

  if aarch64_platform == 'native'
    yanet_aarch64_isa_flag = dpdk_aarch64_isa_flag
  else
    aarch64_isa_flags = {
      'generic': '-march=armv8-a+crc',
      'altra': '-mcpu=neoverse-n1+crypto+rcpc',
      'ampereone': '-mcpu=ampere1+crc+crypto',
    }
    if not aarch64_isa_flags.has_key(aarch64_platform)
      if meson.is_cross_build()
        error(
          'No aarch64 ISA mapping for platform=@0@, taken from the cross file platform property; add one to meson.build.'.format(
            aarch64_platform,
          ),
        )
      else
        error(
          'No aarch64 ISA mapping for dpdk_platform=@0@; add one to meson.build, or use dpdk_platform=generic.'.format(
            aarch64_platform,
          ),
        )
      endif
    endif
    yanet_aarch64_isa_flag = aarch64_isa_flags[aarch64_platform]

    # DPDK probes the compiler at configure time and can fall back to a
    # different switch than the table above expects, so confirm the two
    # actually agree rather than trusting the table blindly.
    if dpdk_aarch64_isa_flag != yanet_aarch64_isa_flag
      error(
        'aarch64 ISA flag mismatch for dpdk_platform=@0@: yanet computed @1@ but DPDK resolved @2@, likely because the compiler probe fell back to a different switch than the table expects; try a newer toolchain, or update the mapping in meson.build if the divergence is genuine.'.format(
          aarch64_platform,
          yanet_aarch64_isa_flag,
          dpdk_aarch64_isa_flag,
        ),
      )
    endif
  endif

  yanet_isa_flag = yanet_aarch64_isa_flag
  yanet_project_args += yanet_isa_flag
endif
add_project_arguments(yanet_project_args, language: 'c')
# Project arguments stay out of subprojects such as DPDK.
add_project_arguments(yanet_c_args, language: 'c')
add_project_link_arguments(yanet_link_args, language: 'c')

yanet_target = host_machine.cpu_family()
if yanet_isa_flag != ''
  yanet_target += ' ' + yanet_isa_flag
endif
yanet_stamp.set_quoted('YANET_TARGET', yanet_target)
yanet_stamp.set_quoted('YANET_DPDK_PLATFORM', yanet_dpdk_platform)
yanet_stamp.set_quoted(
  'YANET_SANITIZERS',
  yanet_sanitizers.length() > 0 ? ','.join(yanet_sanitizers) : 'none',
)
build_stamp = 'yanet_build_stamp.h'
configure_file(output: build_stamp, configuration: yanet_stamp)

# CGO packages by source directory, with what each links and hashes.
#
# 'libraries' are the project libraries a package uses directly; meson adds
# what they depend on. 'archives' are external archives linked as they are,
# with DPDK's system libraries; 'generated_archives' are the same for
# archives a custom target makes, which have no member objects to extract;
# 'inputs' are further native inputs the marker hashes; 'build_tag' is the Go
# build constraint of every file of the package.
cgo_packages = {}

# Every protoc generator in the tree; each Go binary depends on all of them.
#
# No Go binary can then be built before a generated package it imports, and
# the extra edges are cheap: a generator run takes a fraction of a second.
go_proto_targets = []
subdir('common')
subdir('lib')
subdir('devices')
subdir('objects')
subdir('modules')
# Tests that link modules and devices.
subdir('lib/dataplane_ut/tests')
if not dataplane_only
  subdir('lib/controlplane/tests')
endif

if not dataplane_only
  subdir('bindings/go')

  # The Rust SDK fixtures, off by default.
  #
  # Their cgo package is test-only, so the directory is entered after the
  # production closure of the control plane is fixed.
  subdir('lib/rust')

  # Meson computes the link closure of each package for a probe program.
  #
  # The closure becomes the package's generated #cgo flags, so no tracked Go
  # file names a build directory.
  cgo_probes = {}
  cgo_flags_args = []
  foreach package_path, package : cgo_packages
    if package.has_key('libraries') or package.has_key('archives')
      cgo_probes += {package_path: executable(
        'cgo-link-' + package_path.replace('/', '__'),
        files('scripts/cgo_link_probe.c'),
        link_with: package.get('libraries', []),
        link_args: package.has_key('archives') ? dpdk_extra_ldflags : [],
        build_by_default: false,
      )}
    endif
    # External archives link alone, without the libraries meson records
    # behind them.
    foreach archive : package.get('archives', []) + package.get('generated_archives', [])
      cgo_flags_args += ['--archive', package_path, archive.full_path()]
    endforeach
    if package.has_key('build_tag')
      cgo_flags_args += ['--build-tag', package_path, package['build_tag']]
    endif
  endforeach

  # The build directories of one checkout share the generated files, so only
  # build/ in the top-level source tree writes them unless the option says
  # otherwise.
  fs = import('fs')
  cgo_flags_option = get_option('go_cgo_flags')
  if cgo_flags_option.enabled() or (cgo_flags_option.auto()
      and fs.is_samepath(meson.global_build_root(), meson.global_source_root() / 'build'))
    go_cgo_flags = [custom_target(
      'go-cgo-flags',
      output: 'go-cgo-flags.stamp',
      command: [
        python3, files('scripts/go_cgo_flags.py'),
        '--root', meson.project_source_root(),
        '--build-root', meson.global_build_root(),
        '--stamp', '@OUTPUT@',
      ] + cgo_flags_args + cgo_packages.keys(),
      build_always_stale: true,
      build_by_default: true,
    )]
  else
    go_cgo_flags = []
  endif

  cgo_controlplane_deps = []
  cgo_test_deps = []
  foreach package_path, package : cgo_packages
    cgo_libraries = package.get('libraries', []) + package.get('archives', [])
    cgo_native_inputs = (package.get('inputs', []) + package.get('generated_archives', [])
      + cgo_libraries)
    cgo_inputs = cgo_native_inputs
    foreach library : cgo_libraries
      # Thin archives keep member contents in separate object files.
      cgo_inputs += library.extract_all_objects(recursive: true)
    endforeach
    cgo_name = package_path.replace('/', '-') + '-go-cdeps'
    cgo_command = go_cdeps
    if package.has_key('build_tag')
      cgo_command = [
        'sh', '-c',
        'set -eu; sdk_go_flags=$("$1" env GOFLAGS); export GOFLAGS="$sdk_go_flags -tags=$2"; shift 2; exec "$@"',
        'sh', go.full_path(), package['build_tag'],
      ] + cgo_command
    endif
    # Content checks also catch restored mtimes and a deleted package marker.
    cgo_target = custom_target(
      cgo_name,
      input: cgo_inputs,
      output: cgo_name + '.stamp',
      command: cgo_command + [
        '--package', package_path,
        '--output', join_paths(meson.project_source_root(), package_path, 'zz_yanet_cdeps.c'),
        '--stamp', '@OUTPUT@',
      ] + (package.has_key('build_tag') ? ['--build-tag', package['build_tag']] : [])
      + cgo_native_inputs,
      env: yanet_go_env,
      # The marker hashes every archive the package's CGO flags name: the
      # probe builds the project ones, the marker's inputs the external ones.
      depends: go_cgo_flags + (cgo_probes.has_key(package_path) ? [cgo_probes[package_path]] : []),
      build_always_stale: true,
      build_by_default: true,
    )
    cgo_test_deps += cgo_target
    if cgo_controlplane_packages.contains(package_path)
      cgo_controlplane_deps += cgo_target
    endif
  endforeach
endif

subdir('dataplane')

install_data(
  'dataplane.yaml',
  install_dir: '/etc/yanet2',
  rename: 'dataplane-default.yaml',
)

if not dataplane_only
  # Operator proto targets must exist before their consumers are declared.
  #
  # Meson resolves variables in subdir traversal order, so the control-plane
  # and operator binaries below can only depend on these targets if the
  # directories that define them are entered first.
  subdir('operators/bird-adapter/adapterpb')
  subdir('operators/pipeline/operatorpb')
  subdir('operators/route/operatorpb')
  subdir('controlplane')
  subdir('operators')
  subdir('tests')
  alias_target('go-test-prepare', go_proto_targets + cgo_test_deps)
endif

# Each project archive an executable links must be used by its own objects, by
# a symbol its link line forces, or by another archive on that line.
#
# Meson writes introspection data only into the top-level build directory, so
# the check runs only when this is the top-level project.
if not meson.is_subproject()
  test(
    'link-deps',
    python3,
    # -B keeps the helper it imports from writing bytecode into the source tree.
    args: ['-B', files('scripts/check_link_deps.py'), '--build-root', meson.project_build_root()],
    suite: 'lint',
    timeout: 120,
  )
endif
