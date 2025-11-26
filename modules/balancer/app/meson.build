custom_target(
  'balancer_app',         
  output: 'yanet-balancer-app',
  command: [go, 'build', '-C', meson.current_source_dir() + '/cmd/balancer', '-o', '@OUTPUT@', '-buildvcs=false'],
  build_by_default: true,
  install: true,
  install_dir: get_option('bindir'),
  depends: [lib_balancer_cp]
)
