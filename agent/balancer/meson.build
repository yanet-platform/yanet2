agent_balancer_sources = [
    'main.go'
]

custom_target('balancer-agent',
    output : 'balancer-agent',
    input : agent_balancer_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true)
