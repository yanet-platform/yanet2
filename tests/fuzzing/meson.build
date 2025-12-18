# Fuzzing test infrastructure
# This file builds all fuzzing targets registered by modules

# Build all registered fuzzing targets
foreach target_name, target_info : fuzzing_targets
  nofuzz_sources = []
  fuzz_args = []
  
  if get_option('fuzzing').enabled()
    # Full fuzzing mode with libFuzzer
    fuzz_args = ['-fsanitize=fuzzer,address']
  else
    # Reproducer mode - add reproducer main()
    nofuzz_sources += lib_fuzzing_reproducer
  endif
  
  exe = executable(
    target_name,
    c_args: yanet_c_args + fuzz_args,
    link_args: yanet_link_args + fuzz_args,
    sources: [target_info['source']] + nofuzz_sources,
    dependencies: [libdpdk_dep] + target_info['deps'],
    include_directories: [yanet_rootdir, yanet_libdir, target_info['module_dir']],
    link_language: 'c',
  )
  
  # Create a test for the fuzzing target
  # Pass the binary path as argument and set FUZZING_SPLIT_INPUT=1
  test(
    target_name + '_fuzz',
    exe,
    args: [exe.full_path()],
    env: {'FUZZING_SPLIT_INPUT': '1'},
    suite: ['fuzzing'],
    timeout: 30,
  )
endforeach