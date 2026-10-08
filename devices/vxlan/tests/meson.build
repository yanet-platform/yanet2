if rust_builtins_enabled
  vxlan_device_test = executable(
    'vxlan_device_test',
    'device_test.c',
    c_args: yanet_c_args,
    link_args: yanet_link_args + [
      '-Wl,--undefined=new_module_forward',
      '-Wl,--export-dynamic-symbol=new_module_forward',
    ],
    dependencies: [
      lib_dataplane_ut_dep,
      lib_dev_plain_dp_dep,
      lib_dev_vxlan_dp_dep,
      lib_dev_plain_api,
      lib_rust_cp_dep,
      lib_forward_cp_dep,
      lib_logging_dep,
      libdpdk_dep,
    ],
    link_with: [lib_forward_dp],
    include_directories: yanet_rootdir,
  )
  test('vxlan_device', vxlan_device_test, suite: 'devices')
endif
