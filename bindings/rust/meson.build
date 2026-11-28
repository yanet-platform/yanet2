# The Rust module SDK fixtures: the plugins the Go tests of the SDK load and
# the Go views their native builders write.
#
# Off by default: only the SDK's Go tests need them, and they put cargo runs
# into every meson build that enables them. Enabled, the targets run cargo each
# time they are built and cargo decides whether there is work; the Go tests'
# cgo package needs the plugins, so its marker target builds them with the
# rest, and the alias sdk-rust-fixtures builds just them.
sdk_rust_fixtures = get_option('sdk_rust_fixtures')

if sdk_rust_fixtures.enabled()
  rust_sdk_fixture_paths = configuration_data()
  rust_sdk_fixture_paths.set_quoted('YANET_SDK_FIXTURE_PLUGIN_DIR', meson.current_build_dir() / 'plugins')
  rust_sdk_fixture_paths_header = configure_file(
    output: 'yanet_sdk_fixture_paths.h',
    configuration: rust_sdk_fixture_paths,
  )

  cargo = find_program('cargo', required: true)

  # An explicit target keeps the build scripts and the procedural macros
  # uninstrumented in a sanitized build, and fixes the path of the artefact.
  rust_triples = {
    'x86_64': 'x86_64-unknown-linux-gnu',
    'aarch64': 'aarch64-unknown-linux-gnu',
  }
  if host_machine.system() != 'linux' or not rust_triples.has_key(host_machine.cpu_family())
    error('no Rust target for @0@/@1@'.format(host_machine.system(), host_machine.cpu_family()))
  endif
  rust_triple = rust_triples[host_machine.cpu_family()]

  rust_manifest = meson.current_source_dir() / 'Cargo.toml'
  rust_target_dir = meson.current_build_dir() / 'cargo-target'

  # The C flags of the dataplane come from the compilation database of the
  # meson build root.
  rust_env = {
    'CARGO_TARGET_DIR': rust_target_dir,
    'YANET_BUILD_DIR': meson.global_build_root(),
  }

  # An ASan C build needs instrumented Rust: the inline copy of the C block
  # allocator in the shim must lay out red zones as the instrumented C library
  # does.
  #
  # That needs a nightly toolchain, which the environment names with
  # RUSTUP_TOOLCHAIN.
  if yanet_sanitizers.contains('address')
    rust_env += {'RUSTFLAGS': '-Zsanitizer=address -Cforce-frame-pointers=yes'}
  endif

  subdir('plugins')

  # The Go views of the fixture modules' configurations, which the layout
  # generator writes from the layout description of their roots.
  #
  # The generator builds with the profile and target of the plugins, so it
  # describes the layouts they were compiled with. The views instantiate the
  # C views, so the same build writes both into the source tree.
  if native_layout_enabled
    rust_sdk_fixture_layout = custom_target(
      'sdk-fixture-layout',
      output: 'sdk-fixture-layout.stamp',
      command: [
        'sh', '-c', '"$1" run --profile plugin --locked --manifest-path "$2" -p yanet-layout --target "$3" && touch "$4"',
        'sh', cargo, rust_manifest, rust_triple, '@OUTPUT@',
      ],
      env: rust_env,
      build_always_stale: true,
      build_by_default: false,
    )
    go_proto_targets += rust_sdk_fixture_layout
  endif

  alias_target('sdk-rust-fixtures', rust_sdk_fixture_plugins)

  # The Go tests of the SDK, which run the plugins in the dataplane harness.
  #
  # Their package names the plugin directory and the packet's headroom from
  # C headers and links nothing; the paths header and the plugins are hashed
  # so that a change reaches Go's build.
  cgo_packages += {
    'bindings/rust/yanet-sdk-fixture/tests/dataplane': {
      'build_tag': 'sdk_rust_fixtures',
      'inputs': [rust_sdk_fixture_paths_header, rust_sdk_fixture_plugins],
    },
  }
endif
