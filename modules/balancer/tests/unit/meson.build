dependencies = [lib_balancer_state_dep]

interval_counter_test = executable(
  'interval_counter',
  ['interval_counter.c',],
  c_args: yanet_test_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
)

test(
    'interval_counter',
    interval_counter_test,
    suite: ['balancer', 'unit']
)