fuzz_targets = {
  'dscp': [
    lib_packet_dp_dep,
    lib_dscp_dp_dep,
    lib_dscp_cp_dep,
    lib_lib_utils_dep,
  ],
}

nofuzz_sources = []
fuzz_args = []
if get_option('fuzzing').enabled()
  fuzz_args = ['-fsanitize=fuzzer,address']
else
  nofuzz_sources += '../../../tests/common/fuzzing/reproducer.c'
endif

foreach target_name, deps : fuzz_targets
  executable(
    target_name,
    c_args: yanet_c_args + fuzz_args,
    link_args: yanet_link_args + fuzz_args,
    sources: [target_name + '.c'] + nofuzz_sources,
    dependencies: [libdpdk_dep] + deps,
    include_directories: [yanet_rootdir, yanet_libdir, '..'],
    link_language: 'c',
  )
endforeach
