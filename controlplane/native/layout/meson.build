# The Go views of the C structures the native control plane keeps in shared
# memory, for the architecture built.
#
# shmgen generates them into the source tree, as the protobuf packages are,
# from the debug information of a translation unit of the headers that
# define them. The build directories of one checkout share the generated
# files, so only build/ in the top-level source tree writes them, as the cgo
# flags are written; other build directories and architectures write none.

native_layout_archs = {'x86_64': 'amd64', 'aarch64': 'arm64'}
native_layout_gen = []

if native_layout_archs.has_key(host_machine.cpu_family()) and import('fs').is_samepath(
    meson.global_build_root(), meson.global_source_root() / 'build')
  native_layout_arch = native_layout_archs[host_machine.cpu_family()]

  native_layout_types = executable(
    'native-layout-types',
    files('c/types.c'),
    include_directories: include_directories('../../..'),
    dependencies: [libdpdk_inc_dep],
    c_args: ['-g', '-fno-eliminate-unused-debug-types'],
    build_by_default: false,
  )

  # The pointers the dataplane stores as absolute addresses of its own
  # process, and the abs_ pointers the control plane stores relative;
  # nothing in the C declarations says so.
  native_layout_locals = [
    'dp_worker.iterations',
    'dp_worker.rx_count',
    'dp_worker.rx_size',
    'dp_worker.tx_count',
    'dp_worker.tx_size',
    'dp_worker.remote_rx_count',
    'dp_worker.remote_tx_count',
    'dp_worker.local_tx_drops',
    'dp_worker.remote_tx_drops',
    'dp_worker.rx_chained_drops',
    'dp_worker.disposed_count',
    'dp_worker.rx_mempool',
    'dp_worker.rx_bursts',
  ]
  native_layout_relatives = ['module_ectx.abs_runtime_counter_storages']
  native_layout_roots = [
    'dp_config',
    'cp_config',
    'cp_config_gen',
    'agent',
    'cp_module',
    'dp_worker',
    'decap_module_config',
    'classify_attr_line',
    'classify_attr_port',
    'classify_attr_net4',
    'classify_attr_net6',
    'value_table',
    'rule_table',
  ]
  native_layout_dir = meson.current_source_dir()

  # Always stale: shmgen's own sources and the generated files are outside
  # the build graph, and shmgen leaves a file that holds its output alone.
  native_layout_gen = custom_target(
    'native-layout',
    input: native_layout_types,
    output: 'native-layout.stamp',
    command: [
      'sh', '-c', 'go="$1" root="$2" stamp="$3"; shift 3; "$go" -C "$root" run ./controlplane/native/cmd/shmgen "$@" && touch "$stamp"',
      'native-layout',
      go.full_path(),
      meson.project_source_root(),
      '@OUTPUT@',
      '-binary', native_layout_types.full_path(),
      '-package', 'layout',
      '-import', 'github.com/yanet-platform/yanet2/controlplane/native/layout',
      '-out', native_layout_dir / 'layout_' + native_layout_arch + '.go',
      '-test', native_layout_dir / 'layout_' + native_layout_arch + '_test.go',
      '-local', ','.join(native_layout_locals),
      '-relative', ','.join(native_layout_relatives),
    ] + native_layout_roots,
    env: yanet_go_env,
    build_always_stale: true,
    build_by_default: true,
  )
  go_proto_targets += native_layout_gen
endif
