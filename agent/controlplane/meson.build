agent_controlplane_sources = [
    'main.go'
]


custom_target('controlplane-agent',
    output : 'controlplane-agent',
    input : agent_controlplane_sources,
    command : ['go', 'build', '-o', '@OUTPUT@', '@INPUT@'],
    build_by_default: true)
