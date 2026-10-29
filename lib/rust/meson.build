# The Rust module SDK fixtures: what the Go tests of the SDK link and load.
#
# Off by default: only the SDK's Go tests need them, and they put cargo runs
# into every meson build that enables them. Enabled, the targets run cargo each
# time they are built and cargo decides whether there is work; the Go tests'
# cgo package needs them, so its marker target builds them with the rest, and
# the alias sdk-rust-fixtures builds just them.
sdk_rust_fixtures = get_option('sdk_rust_fixtures')

if sdk_rust_fixtures.enabled()
  rust_sdk_fixture_paths = configuration_data()
  rust_sdk_fixture_paths.set_quoted('YANET_SDK_FIXTURE_PLUGIN_DIR', meson.current_build_dir() / 'plugins')
  rust_sdk_fixture_paths_header = configure_file(
    output: 'yanet_sdk_fixture_paths.h',
    configuration: rust_sdk_fixture_paths,
  )

  cargo = find_program('cargo', required: true)
  objcopy = find_program('objcopy', required: true)

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
  #
  # The header lands in the cargo target directory, which outlives a clean of
  # the build outputs, and is copied from there: a cargo run that finds nothing
  # to do does not write it again.
  rust_env = {
    'CARGO_TARGET_DIR': rust_target_dir,
    'YANET_BUILD_DIR': meson.global_build_root(),
    'YANET_SDK_HEADER_DIR': rust_target_dir / 'header',
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

  # The cargo outputs are copied with their times kept, so an unchanged
  # artefact leaves the targets built from it up to date.
  rust_sdk_fixture_api_cargo = custom_target(
    'rust-sdk-fixture-api-cargo',
    output: ['yanet_sdk_fixture_api.raw.a', 'yanet_sdk_fixture_api.h'],
    command: [
      'sh', '-c',
      'set -eu; mkdir -p "$YANET_SDK_HEADER_DIR"; ' +
        '"$1" rustc --profile api --locked --manifest-path "$2" -p yanet-sdk-fixture-api ' +
        '--crate-type staticlib --target "$3"; ' +
        'cp -p "$CARGO_TARGET_DIR/$3/api/libyanet_sdk_fixture_api.a" "$4"; ' +
        'cp -p "$YANET_SDK_HEADER_DIR/yanet_sdk_fixture_api.h" "$5"',
      'sh', cargo, rust_manifest, rust_triple, '@OUTPUT0@', '@OUTPUT1@',
    ],
    env: rust_env,
    build_by_default: false,
    build_always_stale: true,
  )

  # The api archive keeps only its exports global.
  #
  # Two Rust static libraries in one binary share the symbols of the standard
  # library and of the compiler builtins, and ld silently keeps the first copy
  # of each. The archive also carries the C routines of the shim under their
  # dataplane names, which would collide with the dataplane's own in a binary
  # that links both. One relocatable link of the whole archive, with its
  # groups allocated (the shared exception-handling reference breaks the final
  # link otherwise), lets objcopy localise everything but the exports.
  rust_sdk_fixture_api_reloc = custom_target(
    'rust-sdk-fixture-api-reloc',
    input: rust_sdk_fixture_api_cargo[0],
    output: 'yanet_sdk_fixture_api.reloc.o',
    command: cc.cmd_array() + [
      '-r', '-nostdlib', '-Wl,--force-group-allocation',
      '-Wl,--whole-archive', '@INPUT@', '-Wl,--no-whole-archive',
      '-o', '@OUTPUT@',
    ],
    build_by_default: false,
  )

  rust_sdk_fixture_api_local = custom_target(
    'rust-sdk-fixture-api-local',
    input: rust_sdk_fixture_api_reloc,
    output: 'yanet_sdk_fixture_api.local.o',
    command: [
      objcopy, '--wildcard', '--keep-global-symbol=yanet_cp_yanet_sdk_fixture_api__*',
      '@INPUT@', '@OUTPUT@',
    ],
    build_by_default: false,
  )

  rust_sdk_fixture_api_archive = custom_target(
    'rust-sdk-fixture-api',
    input: rust_sdk_fixture_api_local,
    output: 'libyanet_sdk_fixture_api.a',
    command: [ar, 'rcs', '@OUTPUT@', '@INPUT@'],
    build_by_default: false,
  )
  rust_sdk_fixture_api_header = rust_sdk_fixture_api_cargo[1]

  subdir('plugins')

  alias_target(
    'sdk-rust-fixtures',
    rust_sdk_fixture_api_archive,
    rust_sdk_fixture_api_cargo,
    rust_sdk_fixture_plugins,
  )

  # The wrapper of the api exports and the Go tests of the SDK.
  #
  # The archive needs the C routines of the control plane that the api crate
  # calls, the joins of the classification library among them, and the header
  # and the plugins are hashed so that a change reaches Go's build.
  cgo_packages += {
    'lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture': {
      'build_tag': 'sdk_rust_fixtures',
      'libraries': [lib_classify_compiler, lib_config_cp, lib_counters, lib_errors],
      'generated_archives': [rust_sdk_fixture_api_archive],
      'inputs': [rust_sdk_fixture_api_header, rust_sdk_fixture_paths_header, rust_sdk_fixture_plugins],
    },
  }
endif
