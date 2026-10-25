# Register fwstate fuzzing target
fuzzing_targets += {
  'fwstate': {
    'source': files('fwstate.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_fwstate_dp_dep,
      lib_fwstate_objects_dep,
      lib_time_dp_dep,
      lib_counters_dep,
      lib_statemap_dep,
    ],
    'module_dir': '..',
  }
}
