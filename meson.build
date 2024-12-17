project('yanet', 'c',
        meson_version: '>= 0.61',
        default_options: ['buildtype=release',
                          'warning_level=2',
                          'werror=true'])

add_global_arguments(['-march=corei7', '-g', '-O0'], language: 'c')

yanet_rootdir = include_directories('.')

libdpdk = subproject('dpdk', default_options: [
    'platform=generic',
    'cpu_instruction_set=corei7',
    'pkt_mbuf_headroom=256',
    'disable_apps=dumpcap,graph,pdump,proc-info,test-acl,test-bbdev,test-cmdline,test-compress-perf,test-crypto-perf,test-dma-perf,test-eventdev,test-fib,test-flow-perf,test-gpudev,test-mldev,test-pipeline,test-pmd,test-regex,test-sad,test-security-perf',
    'disable_libs=bitratestats,cfgfile,flow_classify,gpudev,gro,gso,kni,jobstats,latencystats,metrics,node,pdump,pipeline,port,power,table,vhost',
    'enable_driver_sdk=true', 'disable_drivers=net/mlx4',
    'default_library=static', 'tests=false' ] )

libdpdk_dep = libdpdk.get_variable('dpdk_dep')
libdpdk_inc_dep = libdpdk_dep.partial_dependency(includes: true)

subdir('common')

subdir('filter')

subdir('lib')

subdir('modules')


subdir('dataplane')
subdir('controlplane')


subdir('sock-send')

