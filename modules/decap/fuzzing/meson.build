fuzz_targets = {
  'decap': [
    lib_testing_module_dp_dep,
    lib_packet_dp_dep,
    lib_decap_dp_dep,
    lib_decap_cp_dep,
  ],
}

sources = []

if get_option('fuzzing').enabled()
  yanet_c_args += ['-fsanitize=fuzzer,address']
  yanet_link_args += ['-fsanitize=fuzzer,address']
else
  sources += 'reproducer.c'
endif

foreach target_name, deps : fuzz_targets
  executable(
    target_name,
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    sources: [target_name + '.c'] + sources,
    dependencies: [libdpdk_dep] + deps,
    include_directories: [yanet_rootdir, yanet_libdir, '..'],
    link_language: 'c',
  )
endforeach
