project(
  'yanet',
  'c',
  meson_version: '>= 0.61',
  version: '0.0.1',
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
if host_machine.cpu_family() in ['x86', 'x86_64']
  yanet_project_args += '-march=' + get_option('cpu_arch')
endif
# aarch64 appends its own ISA flag to yanet_project_args further down,
# once DPDK's subproject() has resolved its ISA baseline.

yanet_conf = configuration_data()
yanet_conf.set('DEBUG_NAT64', get_option('buildtype') == 'debug')
yanet_conf.set('ENABLE_TRACE_LOG', get_option('trace').enabled())
yanet_conf.set('MBUF_MAX_SIZE', get_option('mbuf_max_size'))

# Version and build info
yanet_version = get_option('version') == '' ? meson.project_version() : get_option('version')

cc = meson.get_compiler('c')
yanet_conf.set_quoted('YANET_VERSION', yanet_version)
yanet_conf.set_quoted('YANET_COMPILER_ID', cc.get_id())
yanet_conf.set_quoted('YANET_COMPILER_VERSION', cc.version())
yanet_conf.set_quoted('YANET_BUILD_TYPE', get_option('buildtype'))

# Git commit (if available)
git = find_program('git', required: false)
if git.found()
    git_commit = run_command(git, 'rev-parse', '--short', 'HEAD', check: false)
    if git_commit.returncode() == 0
        yanet_conf.set_quoted('YANET_GIT_COMMIT', git_commit.stdout().strip())
    else
        yanet_conf.set_quoted('YANET_GIT_COMMIT', 'unknown')
    endif
else
    yanet_conf.set_quoted('YANET_GIT_COMMIT', 'unknown')
endif

# Build date
build_date = run_command('date', '-u', '+%Y-%m-%d %H:%M:%S', check: true)
yanet_conf.set_quoted('YANET_BUILD_DATE', build_date.stdout().strip())

build_cfg = 'yanet_build_config.h'
configure_file(output: build_cfg, configuration: yanet_conf)

dataplane_only = get_option('dataplane_only')

if not dataplane_only
  # Define common programs once to avoid duplication
  go = find_program('go', required: true)
  protoc = find_program('protoc', required: true)
  find_program('protoc-gen-go', required: true, version: '>=1.36.5')
  find_program('protoc-gen-go-grpc', required: true, version: '>=1.5.1')
endif

# Build CGO flags based on b_sanitize option
yanet_go_env = {}
yanet_cgo_cflags = []
yanet_cgo_ldflags = []
b_sanitize = get_option('b_sanitize')
if b_sanitize != 'none'
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
        yanet_go_env = {
            'CGO_CFLAGS': cgo_flags,
            'CGO_LDFLAGS': cgo_flags,
        }
        yanet_cgo_cflags += cgo_flags
        yanet_cgo_ldflags += cgo_flags
    endif
endif

yanet_test_c_args = ['-O0']
yanet_c_args = []
yanet_link_args = []
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

  # Add sanitizer flags to CGO for Go binaries
  cgo_flags = '-fsanitize=address -g'
  yanet_go_env = {
      'CGO_CFLAGS': cgo_flags,
      'CGO_LDFLAGS': cgo_flags,
  }
  yanet_cgo_cflags += cgo_flags
  yanet_cgo_ldflags += cgo_flags
endif

yanet_rootdir = include_directories('.')

# Initialize fuzzing targets dictionary
# Modules will populate this with their fuzzing targets
fuzzing_targets = {}

libdpdk_default_options = [
  'platform=' + get_option('dpdk_platform'),
  'pkt_mbuf_headroom=@0@'.format(get_option('pkt_mbuf_headroom')),
  'disable_apps=dumpcap,graph,pdump,proc-info,test-acl,test-bbdev,test-cmdline,test-compress-perf,test-crypto-perf,test-dma-perf,test-eventdev,test-fib,test-flow-perf,test-gpudev,test-mldev,test-pipeline,test-regex,test-sad,test-security-perf',
  'enable_apps=',
  'disable_libs=bitratestats,cfgfile,flow_classify,gpudev,gro,gso,kni,jobstats,latencystats,metrics,node,pdump,pipeline,port,power,table,vhost',
  'enable_driver_sdk=true',
  'enable_drivers=net/mlx5,common/mlx5,bus/auxiliary,net/virtio,net/e1000,net/iavf,common/iavf,net/i40e,net/ring',
  'default_library=static',
  'tests=false',
  'examples=',
  'werror=false',
  'c_args=-Wno-stringop-overflow -Wno-array-bounds -Wno-stringop-overread -Wno-vla-larger-than',
]
if host_machine.cpu_family() in ['x86', 'x86_64']
  libdpdk_default_options += 'cpu_instruction_set=' + get_option('cpu_arch')
endif

libdpdk = subproject(
  'dpdk',
  default_options: libdpdk_default_options,
)

libdpdk_dep = libdpdk.get_variable('dpdk_dep')

# partial_dependency(includes: true) strips the compile args carried by
# dpdk_dep, including -include rte_config.h, so it has to be re-added here.
# DPDK's machine args are deliberately left out.
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
  if meson.is_cross_build()
    aarch64_platform = meson.get_external_property('platform', get_option('dpdk_platform'))
  else
    aarch64_platform = get_option('dpdk_platform')
  endif

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
  dpdk_machine_args = libdpdk.get_variable('machine_args')
  dpdk_aarch64_isa_flag = dpdk_machine_args.length() > 0 ? dpdk_machine_args[0] : ''
  if dpdk_aarch64_isa_flag != yanet_aarch64_isa_flag
    error(
      'aarch64 ISA flag mismatch for dpdk_platform=@0@: yanet computed @1@ but DPDK resolved @2@, likely because the compiler probe fell back to a different switch than the table expects; try a newer toolchain, or update the mapping in meson.build if the divergence is genuine.'.format(
        aarch64_platform,
        yanet_aarch64_isa_flag,
        dpdk_aarch64_isa_flag,
      ),
    )
  endif

  yanet_project_args += yanet_aarch64_isa_flag
endif
add_project_arguments(yanet_project_args, language: 'c')

subdir('common')
subdir('lib')
module_test_dirs = []
subdir('modules')
subdir('devices')
subdir('lib/dataplane_ut/tests')
subdir('dataplane')

if not dataplane_only
  subdir('lib/controlplane/tests')
  # Operator proto targets must exist before their consumers are declared.
  #
  # Meson resolves variables in subdir traversal order, so the control-plane
  # and operator binaries below can only depend on these targets if the
  # directories that define them are entered first.
  subdir('operators/bird-adapter/adapterpb')
  subdir('operators/pipeline/operatorpb')
  subdir('operators/route/operatorpb')
  subdir('operators/forward/operatorpb')
  subdir('operators/decap/operatorpb')
  subdir('controlplane')
  subdir('operators')
  foreach dir : module_test_dirs
    subdir(dir)
  endforeach
  subdir('tests')
endif

# Define a default test setup that excludes large tests
add_test_setup(
  'default',
  exclude_suites: ['large'],
  is_default: true,
)
