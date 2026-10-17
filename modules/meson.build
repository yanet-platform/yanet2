subdir('decap')
subdir('fwstate')
subdir('acl') # depends on fwstate
subdir('route')
subdir('route-mpls')
subdir('forward')
subdir('mirror')
subdir('dscp')
subdir('nat64')
subdir('pdump')
subdir('blackhole')
subdir('unrdup')
subdir('l3b')

foreach extra_module : get_option('extra_modules')
  subdir(extra_module)
endforeach

# Rust-implemented modules stay opt-in: their cargo target resolves a
# Rust toolchain, which a default or dataplane-only build promises not
# to need.
if get_option('with_rust_modules')
  subdir('rblackhole')
endif
