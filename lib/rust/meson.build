# Optional native SDK fixtures share Cargo outputs but keep API archives private.
if get_option('sdk_rust_fixtures').enabled()
  rust_sdk_fixture_paths = configuration_data()
  rust_sdk_fixture_paths.set_quoted('YANET_SDK_FIXTURE_PLUGIN_DIR', meson.current_build_dir() / 'plugins')
  rust_sdk_fixture_paths_header = configure_file(
    output: 'yanet_sdk_fixture_paths.h',
    configuration: rust_sdk_fixture_paths,
  )

  cargo = find_program('cargo', required: true)
  objcopy = find_program('objcopy', required: true)
  nm = find_program('nm', required: true)
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
  rust_env = {
    'CARGO_TARGET_DIR': rust_target_dir,
    'YANET_BUILD_DIR': meson.global_build_root(),
    'YANET_SDK_HEADER_DIR': rust_target_dir / 'header',
  }
  if yanet_sanitizers.contains('address')
    rust_env += {'RUSTFLAGS': '-Zsanitizer=address -Cforce-frame-pointers=yes'}
  endif

  archive = custom_target('yanet-sdk-fixture-api-cargo',
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
  rust_sdk_fixture_api_header = archive[1]

  # Disjoint exports force both localized Rust runtime copies into the Go test.
  rust_sdk_api_archives = []
  foreach view : ['fixture', 'tally']
    ident = 'yanet_sdk_fixture_api_' + view
    keep_tally = view == 'tally' ? '1' : '0'

    # Only actual exported definitions root the relocatable link.
    exports = custom_target(ident + '-exports',
      input: archive[0],
      output: ident + '.exports',
      command: [
        'sh', '-c',
        'set -eu; "$1" -g -P --defined-only "$2" | ' +
          'awk -v stem="$3" -v tally="$4" -v keep_tally="$5" ' +
          '\'index($1, stem) == 1 && ((index($1, tally) == 1) == keep_tally) { print $1 }\' ' +
          '| sort -u > "$6"; test -s "$6"',
        'sh', nm, '@INPUT@', 'yanet_cp_yanet_sdk_fixture_api__',
        'yanet_cp_yanet_sdk_fixture_api__tally_', keep_tally, '@OUTPUT@',
      ],
      build_by_default: false,
    )
    reloc = custom_target(ident + '-reloc',
      input: [archive[0], exports],
      output: ident + '.reloc.o',
      command: [
        'sh', '-c',
        'set -eu; archive=$1; exports=$2; output=$3; shift 3; ' +
          'set -- "$@" -r -nostdlib -Wl,--force-group-allocation -Wl,--gc-sections; ' +
          'while IFS= read -r symbol; do set -- "$@" "-Wl,--undefined=$symbol"; done < "$exports"; ' +
          '"$@" -Wl,--whole-archive "$archive" -Wl,--no-whole-archive -o "$output"',
        'sh', '@INPUT0@', '@INPUT1@', '@OUTPUT@',
      ] + cc.cmd_array(),
      build_by_default: false,
    )
    # Undefined imports keep their binding; dead weak imports are stripped.
    local = custom_target(ident + '-local',
      input: [reloc, exports],
      output: [ident + '.local.o', ident + '.private-definitions'],
      command: [
        'sh', '-c',
        'set -eu; "$1" -g -P --defined-only "$3" | ' +
          'awk \'NR == FNR { keep[$1] = 1; next } !($1 in keep) { print $1 }\' "$4" - ' +
          '| sort -u > "$6"; ' +
          '"$2" --localize-symbols="$6" --strip-unneeded "$3" "$5"',
        'sh', nm, objcopy, '@INPUT0@', '@INPUT1@', '@OUTPUT0@', '@OUTPUT1@',
      ],
      build_by_default: false,
    )
    rust_sdk_api_archives += custom_target(ident + '-archive',
      input: local[0],
      output: 'lib' + ident + '.a',
      command: [ar, 'rcs', '@OUTPUT@', '@INPUT@'],
      build_by_default: false,
    )
  endforeach

  subdir('plugins')
  alias_target('sdk-rust-fixtures', rust_sdk_api_archives, rust_sdk_fixture_plugins)
  cgo_packages += {
    'lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture': {
      'build_tag': 'sdk_rust_fixtures',
      'libraries': [lib_config_cp, lib_counters, lib_errors],
      'generated_archives': rust_sdk_api_archives,
      'inputs': [rust_sdk_fixture_api_header, rust_sdk_fixture_paths_header, rust_sdk_fixture_plugins],
    },
  }
endif
