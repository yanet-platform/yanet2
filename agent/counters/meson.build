agent_counters_sources = [
    'main.go'
]


custom_target('counters-agent',
    output : 'counters-agent',
    input : agent_counters_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true,
      env: get_option('b_coverage') ? {
    'CGO_LDFLAGS': '-lgcov',
  } : {},
    )
