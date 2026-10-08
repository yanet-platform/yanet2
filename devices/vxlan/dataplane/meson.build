# The vxlan device is Rust: its code lives in the builtins archive, and this
# dependency only forces and exports its constructor.
if rust_builtins_enabled
  lib_dev_vxlan_dp_dep = declare_dependency(
    dependencies: lib_rust_dp_builtins_dep,
    link_args: [
      '-Wl,--defsym',
      '-Wl,new_device_vxlan=new_device_vxlan',
      '-Wl,--export-dynamic-symbol=new_device_vxlan',
    ],
  )
else
  lib_dev_vxlan_dp_dep = declare_dependency()
endif
