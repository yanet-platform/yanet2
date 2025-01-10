agent_bootstrap_sources = [
    'main.go'
]


custom_target('bootsrap-agent',
    output : 'bootstrap-agent',
    input : agent_bootstrap_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true)
