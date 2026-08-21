# Build definition overlaid onto the regex checkout, which ships none of
# its own.
#
# regex-capi is a cargo crate, and meson's cargo subproject support
# crashes on this dependency graph: 1.3.2 does not know libc's autolib
# manifest key, and newer meson does not model the cargo workspace
# regex-capi belongs to. Driving cargo from a custom_target sidesteps
# both while keeping the result a normal meson dependency.
project('rure', 'c', version: '0.2.5')

cargo = find_program('cargo', required: true)

# Cargo builds for the machine running it, so a cross build needs the
# target named explicitly or the archive would be for the wrong machine
# and fail at link. A native build says nothing, which keeps it working
# on a toolchain that has only its own target installed.
cargo_target = ''
cargo_subdir = 'release'
if meson.is_cross_build()
  rust_triples = {
    'x86_64': 'x86_64-unknown-linux-gnu',
    'aarch64': 'aarch64-unknown-linux-gnu',
  }
  cpu = host_machine.cpu_family()
  if host_machine.system() != 'linux' or not rust_triples.has_key(cpu)
    error('no rust target known for @0@/@1@'.format(host_machine.system(), cpu))
  endif
  cargo_target = '--target=' + rust_triples[cpu]
  cargo_subdir = rust_triples[cpu] / 'release'
endif

# Cargo names its own output path and has no stable flag to override it,
# so the archive is copied to where meson expects it.
#
# Its inputs are the whole crate tree, which cargo already tracks and
# meson would only track staler, so this runs every build and lets cargo
# decide whether there is work to do.
librure = custom_target(
  'librure',
  output: 'librure.a',
  command: [
    'sh', '-c',
    # --cap-lints is what cargo applies to dependencies on its own, and
    # only withholds from the package being built. This is third-party
    # code either way, and its lints are not actionable here.
    #
    # $5 is unquoted so a native build, which leaves it empty, passes no
    # target argument at all.
    'set -eu; RUSTFLAGS="--cap-lints allow" ' +
      '"$1" build --release --manifest-path "$2" --target-dir "$3" $5; ' +
      'cp "$3/$6/librure.a" "$4"',
    'sh',
    cargo,
    meson.current_source_dir() / 'regex-capi' / 'Cargo.toml',
    meson.current_build_dir() / 'target',
    '@OUTPUT@',
    cargo_target,
    cargo_subdir,
  ],
  build_by_default: true,
  build_always_stale: true,
)

rure_dep = declare_dependency(
  include_directories: include_directories('regex-capi' / 'include'),
  link_with: librure,
  # Reported by rustc --print native-static-libs for this crate.
  link_args: ['-lgcc_s', '-lutil', '-lrt', '-lpthread', '-lm', '-ldl'],
)

meson.override_dependency('rure', rure_dep)
