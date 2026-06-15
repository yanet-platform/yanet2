# Register mirror fuzzing target
fuzzing_targets += {
  'mirror': {
    'source': files('mirror.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_mirror_dp_dep,
      lib_mirror_cp_dep,
      lib_worker_dp_dep,
      lib_config_dp_dep,
      lib_lib_utils_dep,
    ],
    'module_dir': '..',
  }
}
