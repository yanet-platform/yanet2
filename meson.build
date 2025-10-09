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

yanet_test_c_args = ['-O0']
yanet_c_args = []
yanet_link_args = []
if get_option('fuzzing').enabled()
  fuzz_args = [
    '-fsanitize=fuzzer-no-link,address',
    '-fsanitize-coverage=edge,indirect-calls,trace-cmp',
    '-fprofile-instr-generate',
    '-fcoverage-mapping',
  ]

  # For fuzzing builds, all libraries should be instrumented with coverage counters.
  yanet_c_args += fuzz_args
  yanet_link_args += fuzz_args
endif
yanet_rootdir = include_directories('.')

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
subdir('controlplane')
subdir('dataplane')
subdir('sock-send')
subdir('tests')
subdir('coordinator')
