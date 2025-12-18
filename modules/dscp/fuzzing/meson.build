# Register dscp fuzzing target
fuzzing_targets += {
  'dscp': {
    'source': files('dscp.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_dscp_dp_dep,
      lib_dscp_cp_dep,
      lib_lib_utils_dep,
    ],
    'module_dir': '..',
  }
}
