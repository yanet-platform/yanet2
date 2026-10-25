# Register forward fuzzing target
fuzzing_targets += {
  'forward': {
    'source': files('forward.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_forward_dp_dep,
      lib_forward_cp_dep,
      lib_config_cp_dep,
      lib_counters_dep,
    ],
    'module_dir': '..',
  }
}
