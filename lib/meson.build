subdir('logging')
subdir('errors')
subdir('counters')
subdir('dataplane')
subdir('controlplane/config')
# The dataplane config library calls into the controlplane config library,
# and the agent's attach test links the dataplane one.
subdir('dataplane/config')
subdir('controlplane/agent')
subdir('utils')
subdir('filter') # depends on dataplane and utils libs
subdir('statemap') # generic layered hash map core
subdir('classify') # compositional classification library, parallel to filter
subdir('fwstate') # depends on the statemap lib
subdir('l3state')
subdir('fuzzing')
subdir('dataplane_ut')
# The tests use the logging, counters, dataplane, controlplane and utils libs.
subdir('tests')
