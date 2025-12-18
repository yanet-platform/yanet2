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

add_global_arguments(
  ['-march=corei7', '-g', '-D_GNU_SOURCE',],
  language: 'c',
)

yanet_conf = configuration_data()
yanet_conf.set('DEBUG_NAT64', get_option('buildtype') == 'debug')
yanet_conf.set('ENABLE_TRACE_LOG', get_option('trace').enabled())
yanet_conf.set('MBUF_MAX_SIZE', get_option('mbuf_max_size'))

build_cfg = 'yanet_build_config.h'
configure_file(output: build_cfg, configuration: yanet_conf)

# Define common programs once to avoid duplication
go = find_program('go', required: true)
protoc = find_program('protoc', required: true)

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

libdpdk = subproject(
  'dpdk',
  default_options: [
    'platform=generic',
    'cpu_instruction_set=corei7',
    'pkt_mbuf_headroom=@0@'.format(get_option('pkt_mbuf_headroom')),
    'disable_apps=dumpcap,graph,pdump,proc-info,test-acl,test-bbdev,test-cmdline,test-compress-perf,test-crypto-perf,test-dma-perf,test-eventdev,test-fib,test-flow-perf,test-gpudev,test-mldev,test-pipeline,test-regex,test-sad,test-security-perf',
    'enable_apps=',
    'disable_libs=bitratestats,cfgfile,flow_classify,gpudev,gro,gso,kni,jobstats,latencystats,metrics,node,pdump,pipeline,port,power,table,vhost',
    'enable_driver_sdk=true',
    'enable_drivers=net/mlx5,common/mlx5,bus/auxiliary,net/virtio,net/e1000',
    'default_library=static',
    'tests=false',
    'examples=',
    'werror=false',
    'c_args=-Wno-stringop-overflow -Wno-array-bounds -Wno-stringop-overread -Wno-vla-larger-than',
  ],
)

libdpdk_dep = libdpdk.get_variable('dpdk_dep')
libdpdk_inc_dep = libdpdk_dep.partial_dependency(includes: true)

subdir('common')
subdir('lib')
subdir('filter')
subdir('modules')
subdir('devices')
subdir('controlplane')
subdir('dataplane')
subdir('mock')
subdir('sock-send')
subdir('tests')

# Define a default test setup that excludes large tests
add_test_setup(
  'default',
  exclude_suites: ['large'],
  is_default: true,
)
