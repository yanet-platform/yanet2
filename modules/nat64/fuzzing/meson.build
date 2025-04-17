fuzz_targets = {
  'nat64w': [
    lib_testing_module_dp_dep,
    lib_packet_dp_dep,
    lib_nat64_dp_dep,
    lib_nat64_cp_dep,
    libdpdk_inc_dep,
  ],
}

yanet_c_args += ['-fsanitize=fuzzer,address']
yanet_link_args += ['-fsanitize=fuzzer,address']

foreach target_name, deps : fuzz_targets
  executable(
    target_name,
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    sources: [target_name + '.c'],
    dependencies: [libdpdk_dep] + deps,
    include_directories: [yanet_rootdir, yanet_libdir, '..'],
    link_language: 'c',
  )
endforeach
