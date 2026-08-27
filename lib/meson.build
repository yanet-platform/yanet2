# errors and cancellation are entered from the root before common, which
# carries them as dependencies.
subdir('logging')
# The library itself is entered from the root; its test needs logging.
subdir('cancellation/tests')
subdir('counters')
subdir('dataplane')
subdir('controlplane')
subdir('utils')
subdir('filter') # depends on dataplane and utils libs
subdir('fwstate') # depends on dataplane lib
subdir('fuzzing')
subdir('dataplane_ut')
subdir('tests') # depends on utils lib
