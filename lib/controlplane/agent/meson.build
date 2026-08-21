dependencies = [
  lib_common_dep,
  lib_config_dp_dep,
  lib_config_cp_dep,
  lib_errors_dep,
]

sources = files(
  'agent.c',
)

lib_agent_cp = static_library(
  'agent',
  sources,
  c_args: yanet_c_args,
  link_args: yanet_link_args,
  dependencies: dependencies,
  install: false,
)

lib_agent_cp_dep = declare_dependency(
  link_with: lib_agent_cp,
  dependencies: [lib_errors_dep],
  include_directories: yanet_rootdir,
)

# The counter read is a separate library because it is the only part of the
# agent API that needs the name matcher, and through it a Rust toolchain.
# Keeping it apart is what lets a dataplane-only build skip both.
if not dataplane_only
  lib_agent_counters = static_library(
    'agent_counters',
    files('counters.c'),
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: dependencies + [lib_counter_pattern_dep],
    install: false,
  )

  lib_agent_counters_dep = declare_dependency(
    link_with: lib_agent_counters,
    dependencies: [lib_agent_cp_dep, lib_counter_pattern_dep],
    include_directories: yanet_rootdir,
  )

  test_agent_attach = executable(
    'agent_attach_test',
    'agent_test.c',
    c_args: yanet_c_args,
    link_args: yanet_link_args,
    dependencies: [
      lib_agent_counters_dep,
      lib_config_dp_dep,
      lib_config_cp_dep,
      lib_logging_dep,
    ],
    include_directories: yanet_rootdir,
    link_language: 'c',
  )
  test('agent_attach', test_agent_attach, suite: ['lib'])
endif
