# Register nat64w fuzzing target
fuzzing_targets += {
  'nat64w': {
    'source': files('nat64w.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_nat64_dp_dep,
      lib_nat64_cp_dep,
      libdpdk_inc_dep,
      lib_lib_utils_dep,
    ],
    'module_dir': '..',
  }
}
