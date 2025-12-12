deps = [lib_common_dep, lib_packet_dp_dep, lib_filter_dep, lib_filter_utils_dep, libdpdk_inc_dep, libdpdk_dep]

compiler = executable('compiler',
    ['compiler.c'],
    c_args: yanet_test_c_args,
    link_args: yanet_link_args,
    dependencies: deps,
    include_directories: yanet_rootdir,
    link_language: 'c'
)

filter = executable('filter',
    ['filter.c'],
    c_args: yanet_test_c_args,
    link_args: yanet_link_args,
    dependencies: deps,
    include_directories: yanet_rootdir,
    link_language: 'c'
)

python = find_program('python3', required: true)

test(
    'shm',
    python,
    args: [files('test.py'), meson.current_build_dir() + '/compiler', meson.current_build_dir() + '/filter'],
    depends: [compiler, filter],
    timeout: 10,
    suite: ['filter']
)