# Register fwstate fuzzing target
fuzzing_targets += {
  'fwstate': {
    'source': files('fwstate.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_fwstate_dp_dep,
      lib_fwstate_dep,
      lib_lib_utils_dep,
      lib_time_dp_dep,
    ],
    'module_dir': '..',
  }
}
