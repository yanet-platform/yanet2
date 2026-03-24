dependencies = [lib_balancer_state_dep]
includes = include_directories('../../..', '../../../', '../../../../')

interval_counter_test = executable(
  'interval_counter',
  [
    'interval_counter.c',
    '../../controlplane/state/interval_counter.c',
  ],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  include_directories: includes,
  dependencies: dependencies,
)
test(
    'interval_counter',
    interval_counter_test,
    suite: ['balancer', 'unit']
)

active_sessions_tracker_test = executable(
  'active_sessions_tracker',
  ['active_sessions_tracker.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  include_directories: includes,
  dependencies: dependencies,
)
test(
    'active_sessions_tracker',
    active_sessions_tracker_test,
    suite: ['balancer', 'unit']
)