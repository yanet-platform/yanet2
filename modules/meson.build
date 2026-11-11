# The public modules. The Makefile reads this list; the dataplane's default
# module set and the .gitignore whitelist repeat it by hand.
subdir('decap')
subdir('fwstate')
subdir('acl')
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
