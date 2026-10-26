shm_deps = [
  lib_common_dep,
  lib_filter_compiler_dep,
  lib_lib_utils_dep,
  lib_errors_dep,
  lib_logging_dep,
  libdpdk_inc_dep,
]

shm_compiler = executable('compiler',
    ['compiler.c'],
    c_args: yanet_test_c_args,
    dependencies: shm_deps,
    include_directories: yanet_rootdir,
)

shm_filter = executable('filter',
    ['filter.c'],
    c_args: yanet_test_c_args,
    dependencies: shm_deps,
    include_directories: yanet_rootdir,
)

test(
    'shm',
    python3,
    args: [files('test.py'), shm_compiler, shm_filter],
    timeout: 10,
    suite: ['filter']
)