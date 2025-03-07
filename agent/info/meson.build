agent_info_sources = [
    'main.go'
]


custom_target('info-agent',
    output : 'info-agent',
    input : agent_info_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true)
