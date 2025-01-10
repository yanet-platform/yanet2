agent_route_sources = [
    'main.go'
]

custom_target('route-agent',
    output : 'route-agent',
    input : agent_route_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true)
