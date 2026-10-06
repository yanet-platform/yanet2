# The dataplane half is Rust: cargo builds the static archive, meson
# links it into the dataplane binary like any module's C library.
#
# The whole module is gated on the rust_modules option, so a default or
# dataplane-only build still resolves no Rust toolchain — the same
# promise lib/counters' rure block makes for the control plane.

cargo = find_program('cargo', required: true)

# A cross build must name the target, or the archive is built for the
# machine running cargo and fails at link. A native build names none,
# which keeps it working on a toolchain that carries only its own target.
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

# Cargo names its own output path, so the archive is copied to where
# meson expects it. The crate tree is cargo's to track, so this runs
# every build and cargo decides whether there is work to do.
#
# --cap-lints is what cargo applies to dependencies on its own and only
# withholds from the package being built; first-party lints stay on.
lib_rblackhole_rs = custom_target(
  'librblackhole_rs',
  output: 'librblackhole_rs.a',
  command: [
    'sh', '-c',
    'set -eu; RUSTFLAGS="--cap-lints allow" ' +
      '"$1" build --release --manifest-path "$2" --target-dir "$3" $5; ' +
      'cp "$3/$6/libyanet_rblackhole_dp.a" "$4"',
    'sh',
    cargo,
    meson.project_source_root() / 'modules' / 'rblackhole' / 'rust' / 'Cargo.toml',
    meson.current_build_dir() / 'rblackhole-target',
    '@OUTPUT@',
    cargo_target,
    cargo_subdir,
  ],
  build_by_default: true,
  build_always_stale: true,
)

lib_rblackhole_dp_dep = declare_dependency(
  link_with: lib_rblackhole_rs,
  # Reported by rustc --print native-static-libs for this crate.
  link_args: [
    '-Wl,--defsym,new_module_rblackhole=new_module_rblackhole',
    '-Wl,--export-dynamic-symbol=new_module_rblackhole',
    '-lgcc_s',
    '-lutil',
    '-lrt',
    '-lpthread',
    '-lm',
    '-ldl',
  ],
)
