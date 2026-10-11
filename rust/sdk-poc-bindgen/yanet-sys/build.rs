//! Build script of the YANET2 sys crate.
//!
//! One source of C truth: the include directories, defines and forced
//! includes of the meson decap module target (from `compile_commands.json`)
//! drive bindgen, the C shim compilation and the gcc layout probe, and are
//! re-exported to dependents as `DEP_YANET_*` metadata.

use core::fmt::Write as _;
use std::{
    collections::{BTreeMap, BTreeSet},
    env, fs,
    path::{Path, PathBuf},
    process::Command,
};

/// Translation unit whose meson flags every C consumer of this crate reuses.
const REFERENCE_TU: &str = "modules/decap/dataplane/dataplane.c";

/// C aggregates whose gcc layout is cross-checked against the Rust bindings.
///
/// Every aggregate the SDK projects into through `layout!` must be listed;
/// the list may hold more.
const GCC_CHECKED: &[(&str, &str)] = &[
    ("struct", "lpm"),
    ("struct", "lpm_page"),
    ("union", "lpm_value"),
    ("struct", "memory_context"),
    ("struct", "cp_module"),
    ("struct", "registry_item"),
    ("struct", "module_ectx"),
    ("struct", "module"),
    ("struct", "packet"),
    ("struct", "packet_list"),
    ("struct", "packet_front"),
    ("struct", "network_header"),
    ("struct", "transport_header"),
    ("struct", "decap_module_config"),
    ("struct", "rte_mbuf"),
];

/// Scalar types that never hold a pointer.
///
/// An integer field that stores an address or an offset is invisible to the
/// check; such fields must be found by reading the C header.
const PRIMITIVES: &[&str] = &[
    "u8",
    "u16",
    "u32",
    "u64",
    "u128",
    "usize",
    "i8",
    "i16",
    "i32",
    "i64",
    "i128",
    "isize",
    "bool",
    "f32",
    "f64",
    "c_char",
    "c_schar",
    "c_uchar",
    "c_short",
    "c_ushort",
    "c_int",
    "c_uint",
    "c_long",
    "c_ulong",
    "c_longlong",
    "c_ulonglong",
    "c_float",
    "c_double",
];

/// Rust keywords bindgen escapes with a trailing underscore.
const RUST_KEYWORDS: &[&str] = &[
    "as", "async", "await", "box", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false",
    "fn", "for", "gen", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "priv", "pub", "ref",
    "return", "self", "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while", "yield",
];

/// Compiler invocation of the reference translation unit, reduced to the
/// options that change how headers are read.
struct CFlags {
    compiler: PathBuf,
    includes: Vec<PathBuf>,
    defines: Vec<String>,
    forced_includes: Vec<String>,
    machine: Vec<String>,
}

impl CFlags {
    fn from_compile_commands(build_dir: &Path, root: &Path) -> Self {
        let db_path = build_dir.join("compile_commands.json");
        println!("cargo:rerun-if-changed={}", db_path.display());
        let db = fs::read_to_string(&db_path).unwrap_or_else(|err| {
            panic!(
                "yanet-sys needs a configured meson build directory (run `meson setup build` at the repository root \
                 or set YANET_BUILD_DIR): cannot read {}: {err}",
                db_path.display()
            )
        });
        let db: serde_json::Value = serde_json::from_str(&db).expect("compile_commands.json is not valid JSON");
        let reference = root.join(REFERENCE_TU);
        let entry = db
            .as_array()
            .expect("compile_commands.json is not an array")
            .iter()
            .find(|entry| {
                let file = entry["file"].as_str().unwrap_or_default();
                let dir = Path::new(entry["directory"].as_str().unwrap_or_default());
                dir.join(file).canonicalize().ok() == reference.canonicalize().ok()
            })
            .unwrap_or_else(|| panic!("{REFERENCE_TU} is missing from {}", db_path.display()));
        let dir = PathBuf::from(entry["directory"].as_str().expect("entry without directory"));
        let command = entry["command"].as_str().expect("entry without command");

        let mut tokens = command.split_whitespace();
        let compiler = PathBuf::from(tokens.next().expect("empty compile command"));
        let mut flags = Self {
            compiler,
            includes: Vec::new(),
            defines: Vec::new(),
            forced_includes: Vec::new(),
            machine: Vec::new(),
        };
        while let Some(token) = tokens.next() {
            if let Some(include) = token.strip_prefix("-I") {
                let path = dir.join(include);
                // Meson adds per-target private directories that may not exist
                // yet; they never hold a header the bindings need.
                if path.is_dir() {
                    flags
                        .includes
                        .push(path.canonicalize().expect("canonical include path"));
                }
            } else if token.starts_with("-D") {
                flags.defines.push(token.to_owned());
            } else if token == "-include" {
                flags
                    .forced_includes
                    .push(tokens.next().expect("-include without a file").to_owned());
            } else if token.starts_with("-march=") || token.starts_with("-std=") {
                flags.machine.push(token.to_owned());
            }
        }
        flags
    }

    /// Flags that reproduce how the reference unit reads headers.
    fn header_args(&self) -> Vec<String> {
        let mut args = Vec::new();
        for include in &self.includes {
            args.push(format!("-I{}", include.display()));
        }
        args.extend(self.defines.iter().cloned());
        for forced in &self.forced_includes {
            args.push("-include".to_owned());
            args.push(forced.clone());
        }
        args.extend(self.machine.iter().cloned());
        args
    }
}

fn main() {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let out_dir = PathBuf::from(env::var("OUT_DIR").unwrap());
    println!("cargo:rerun-if-env-changed=YANET_ROOT");
    println!("cargo:rerun-if-env-changed=YANET_BUILD_DIR");
    println!("cargo:rerun-if-env-changed=LIBCLANG_PATH");
    println!("cargo:rerun-if-env-changed=CLANG_PATH");

    let root = env::var_os("YANET_ROOT")
        .map(PathBuf::from)
        .unwrap_or_else(|| manifest_dir.join("../../.."))
        .canonicalize()
        .expect("repository root");
    let build_dir = env::var_os("YANET_BUILD_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| root.join("build"));
    let flags = CFlags::from_compile_commands(&build_dir, &root);
    let shim_dir = manifest_dir.join("shim");

    let bindings_path = out_dir.join("bindings.rs");
    generate_bindings(&flags, &shim_dir, &bindings_path);

    let items = parse_bindings(&bindings_path);
    fs::write(out_dir.join("bindgen_fields.rs"), render_field_table(&items)).unwrap();
    let gcc_fields = probe_gcc_layout(&flags, &items, &out_dir);
    fs::write(out_dir.join("gcc_layout.rs"), render_gcc_layout(&gcc_fields)).unwrap();
    fs::write(out_dir.join("rust_layout.rs"), render_rust_layout(&gcc_fields)).unwrap();

    compile_shim(&flags, &root, &shim_dir);
    if env::var_os("CARGO_FEATURE_TESTING").is_some() {
        generate_fixtures(&flags, &shim_dir, &out_dir);
    }

    // Single source of C flags for dependents' build scripts.
    let join = |values: Vec<String>| values.join(";");
    println!("cargo:root={}", root.display());
    println!(
        "cargo:include={}",
        join(flags.includes.iter().map(|p| p.display().to_string()).collect())
    );
    println!("cargo:defines={}", join(flags.defines.clone()));
    println!("cargo:forced_includes={}", join(flags.forced_includes.clone()));
    println!("cargo:machine={}", join(flags.machine.clone()));
    println!("cargo:compiler={}", flags.compiler.display());
}

fn generate_bindings(flags: &CFlags, shim_dir: &Path, out: &Path) {
    let bindings = bindgen::Builder::default()
        .header(shim_dir.join("bindings.h").display().to_string())
        .clang_args(flags.header_args())
        .clang_arg(format!("-I{}", shim_dir.display()))
        .parse_callbacks(Box::new(bindgen::CargoCallbacks::new()))
        .rust_target(bindgen::RustTarget::stable(88, 0).expect("supported Rust target"))
        .rust_edition(bindgen::RustEdition::Edition2024)
        .use_core()
        .ctypes_prefix("core::ffi")
        .derive_debug(false)
        .derive_default(false)
        .wrap_unsafe_ops(true)
        .generate_comments(false)
        .layout_tests(true)
        .allowlist_type("lpm")
        .allowlist_type("lpm_page")
        .allowlist_type("lpm_value")
        .allowlist_type("cp_module")
        .allowlist_type("module_ectx")
        .allowlist_type("module")
        .allowlist_type("packet")
        .allowlist_type("packet_list")
        .allowlist_type("packet_front")
        .allowlist_type("decap_module_config")
        .allowlist_type("rte_mbuf")
        .allowlist_function("packet_decap")
        .allowlist_function("parse_packet")
        .allowlist_function("yanet_sys_test_.*")
        .allowlist_function("yanet_sys_cp_.*")
        .allowlist_function("cp_module_init_layout")
        .allowlist_function("cp_module_fini")
        .allowlist_function("cp_module_try_destroy")
        .allowlist_function("yanet_error_message")
        .allowlist_function("yanet_error_free")
        .allowlist_var("LPM_.*")
        .allowlist_var("YANET_MODULE_ABI_VERSION")
        .allowlist_var("RTE_PKTMBUF_HEADROOM")
        .allowlist_var("PACKET_TRANSPORT_HEADER_UNAVAILABLE")
        .allowlist_type("packet_flag")
        // Reached only behind pointers the SDK never follows.
        .opaque_type("rte_mempool")
        .opaque_type("rte_mbuf_ext_shared_info")
        .opaque_type("dp_config")
        .opaque_type("cp_config")
        .opaque_type("agent_storage")
        .opaque_type("dp_worker")
        .opaque_type("config_gen_ectx")
        .opaque_type("device_entry_ectx")
        .opaque_type("counter_storage")
        .opaque_type("block_allocator")
        .generate()
        .expect("bindgen failed");
    bindings.write_to_file(out).expect("write bindings");
}

/// What a C field carries, as far as the strict pointer check cares.
#[derive(Clone, Debug)]
enum FieldType {
    Pointer,
    Plain,
    Named(String),
    Opaque,
}

#[derive(Debug)]
struct Aggregate {
    is_union: bool,
    fields: Vec<(String, FieldType)>,
}

#[derive(Default)]
struct Items {
    aggregates: BTreeMap<String, Aggregate>,
    aliases: BTreeMap<String, syn::Type>,
}

impl Items {
    /// Classifies a bindgen field type, failing the build on any type form
    /// or name it does not recognise rather than guessing it holds no
    /// pointer.
    fn classify(&self, ty: &syn::Type) -> FieldType {
        match ty {
            syn::Type::Ptr(_) | syn::Type::BareFn(_) => FieldType::Pointer,
            // A zero-length array is a layout marker without bytes.
            syn::Type::Array(array) if is_zero_length(&array.len) => FieldType::Plain,
            syn::Type::Array(array) => self.classify(&array.elem),
            syn::Type::Path(path) => {
                let segment = path.path.segments.last().expect("empty type path");
                let name = segment.ident.to_string();
                let generic = match &segment.arguments {
                    syn::PathArguments::AngleBracketed(args) => args.args.iter().find_map(|arg| match arg {
                        syn::GenericArgument::Type(ty) => Some(ty),
                        _ => None,
                    }),
                    _ => None,
                };
                match name.as_str() {
                    // Function pointers, flexible array members and union
                    // members wrap the type that matters.
                    "Option" | "__IncompleteArrayField" | "__BindgenUnionField" | "ManuallyDrop" => {
                        self.classify(generic.unwrap_or_else(|| panic!("`{name}` without a type argument")))
                    }
                    // Bitfield storage holds integers only.
                    "__BindgenBitfieldUnit" => FieldType::Plain,
                    _ if self.aggregates.contains_key(&name) => FieldType::Named(name),
                    _ if PRIMITIVES.contains(&name.as_str()) => FieldType::Plain,
                    _ => match self.aliases.get(&name) {
                        Some(target) => self.classify(target),
                        None => panic!("unrecognised bindgen field type `{name}`: teach the pointer check about it"),
                    },
                }
            }
            other => panic!(
                "unrecognised bindgen field type form `{}`: teach the pointer check about it",
                quote_type(other)
            ),
        }
    }

    /// Whether an aggregate holds a pointer anywhere inside its bytes.
    ///
    /// An opaque aggregate hides its fields from bindgen, so it counts as
    /// pointer-carrying: the check must never trust what it cannot see.
    fn carries_pointer(&self, name: &str, visiting: &mut BTreeSet<String>) -> bool {
        if !visiting.insert(name.to_owned()) {
            return false;
        }
        let Some(aggregate) = self.aggregates.get(name) else {
            return false;
        };
        aggregate
            .fields
            .iter()
            .any(|(_, ty)| self.field_carries_pointer(ty, visiting))
    }

    fn field_carries_pointer(&self, ty: &FieldType, visiting: &mut BTreeSet<String>) -> bool {
        match ty {
            FieldType::Pointer | FieldType::Opaque => true,
            FieldType::Plain => false,
            FieldType::Named(name) => self.carries_pointer(name, visiting),
        }
    }
}

fn quote_type(ty: &syn::Type) -> String {
    match ty {
        syn::Type::Tuple(_) => "tuple".to_owned(),
        syn::Type::Reference(_) => "reference".to_owned(),
        syn::Type::Slice(_) => "slice".to_owned(),
        _ => "other".to_owned(),
    }
}

fn is_zero_length(len: &syn::Expr) -> bool {
    match len {
        syn::Expr::Lit(syn::ExprLit { lit: syn::Lit::Int(int), .. }) => int.base10_digits() == "0",
        _ => false,
    }
}

/// Aggregate name, whether it is a union, and its named fields.
type RawAggregate = (String, bool, Vec<(String, syn::Type)>);

fn parse_bindings(path: &Path) -> Items {
    let source = fs::read_to_string(path).unwrap();
    let file = syn::parse_file(&source).expect("bindgen output parses");
    let mut items = Items::default();
    let mut raw: Vec<RawAggregate> = Vec::new();
    for item in &file.items {
        match item {
            // bindgen's generic helpers (bitfield units, flexible arrays) are
            // classified where they are used.
            syn::Item::Struct(item) if !item.generics.params.is_empty() => {}
            syn::Item::Struct(item) => {
                let fields = item
                    .fields
                    .iter()
                    .filter_map(|f| f.ident.as_ref().map(|i| (i.to_string(), f.ty.clone())))
                    .collect();
                raw.push((item.ident.to_string(), false, fields));
            }
            syn::Item::Union(item) => {
                let fields = item
                    .fields
                    .named
                    .iter()
                    .map(|f| (f.ident.as_ref().unwrap().to_string(), f.ty.clone()))
                    .collect();
                raw.push((item.ident.to_string(), true, fields));
            }
            syn::Item::Type(alias) => {
                items.aliases.insert(alias.ident.to_string(), (*alias.ty).clone());
            }
            _ => {}
        }
    }
    for (name, is_union, _) in &raw {
        items.aggregates.insert(
            name.clone(),
            Aggregate {
                is_union: *is_union,
                fields: Vec::new(),
            },
        );
    }
    for (name, _, fields) in raw {
        let classified = fields
            .iter()
            .map(|(field, ty)| {
                let kind = if field == "_bindgen_opaque_blob" {
                    FieldType::Opaque
                } else {
                    items.classify(ty)
                };
                (field.clone(), kind)
            })
            .collect();
        items.aggregates.get_mut(&name).unwrap().fields = classified;
    }
    items
}

/// Emits the table of every bindgen field and a const check function with
/// one branch per pointer-carrying field.
///
/// Every `layout!` invocation, including the negative compile tests, runs
/// the check against its own declaration table; the panic message names the
/// unclassified field.
fn render_field_table(items: &Items) -> String {
    let mut table = String::from("/// Every field bindgen produced, with whether its bytes hold a pointer.\n");
    table.push_str("pub const BINDGEN_FIELDS: &[crate::layout::CField] = &[\n");
    let mut checks = String::new();
    for (name, aggregate) in &items.aggregates {
        for (field, ty) in &aggregate.fields {
            let carries = items.field_carries_pointer(ty, &mut BTreeSet::new());
            let direct = matches!(ty, FieldType::Pointer);
            writeln!(
                table,
                "    crate::layout::CField {{ aggregate: {name:?}, field: {field:?}, carries_pointer: {carries}, \
                 is_pointer: {direct} }},"
            )
            .unwrap();
            if carries {
                writeln!(
                    checks,
                    "    if crate::layout::declares(decl, {name:?}) && !crate::layout::classifies(decl, {name:?}, \
                     {field:?}) {{\n        panic!(\"layout!: pointer-carrying field `{name}.{field}` is not \
                     classified (rel, abs, ffi, embed or opaque)\");\n    }}"
                )
                .unwrap();
            }
        }
    }
    table.push_str("];\n\n");
    table.push_str(
        "/// Fails const evaluation when a declared aggregate leaves a\n/// pointer-carrying field unclassified.\npub const fn check_unclassified(decl: &[crate::layout::Decl]) {\n",
    );
    table.push_str(&checks);
    table.push_str("}\n");
    table
}

/// One field as gcc lays it out: aggregate keyword, aggregate, Rust field
/// name, C field name, offset and size.
struct GccField {
    keyword: &'static str,
    aggregate: String,
    rust_field: Option<String>,
    offset: usize,
    size: usize,
}

fn probe_gcc_layout(flags: &CFlags, items: &Items, out_dir: &Path) -> Vec<GccField> {
    let mut program =
        String::from("#include \"bindings.h\"\n#include <stddef.h>\n#include <stdio.h>\n\nint\nmain(void) {\n");
    let mut expected: Vec<(&'static str, String, Option<String>)> = Vec::new();
    for (keyword, aggregate) in GCC_CHECKED {
        let info = items
            .aggregates
            .get(*aggregate)
            .unwrap_or_else(|| panic!("{keyword} {aggregate} is not in the bindings"));
        assert_eq!(
            info.is_union,
            *keyword == "union",
            "{aggregate}: aggregate keyword mismatch"
        );
        writeln!(
            program,
            "\tprintf(\"%zu %zu\\n\", sizeof({keyword} {aggregate}), _Alignof({keyword} {aggregate}));"
        )
        .unwrap();
        expected.push((keyword, (*aggregate).to_owned(), None));
        for (field, _) in &info.fields {
            if field.starts_with("__bindgen") || field.starts_with("_bitfield") || field.starts_with("_bindgen") {
                continue;
            }
            let c_field = match field.strip_suffix('_') {
                Some(stripped) if RUST_KEYWORDS.contains(&stripped) => stripped,
                _ => field.as_str(),
            };
            writeln!(
                program,
                "\tprintf(\"%zu %zu\\n\", offsetof({keyword} {aggregate}, {c_field}), sizeof((({keyword} \
                 {aggregate} *)0)->{c_field}));"
            )
            .unwrap();
            expected.push((keyword, (*aggregate).to_owned(), Some(field.clone())));
        }
    }
    program.push_str("\treturn 0;\n}\n");
    let source = out_dir.join("gcc_layout_probe.c");
    let binary = out_dir.join("gcc_layout_probe");
    fs::write(&source, program).unwrap();
    let shim_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap()).join("shim");
    let status = Command::new(&flags.compiler)
        .args(flags.header_args())
        .arg(format!("-I{}", shim_dir.display()))
        .arg("-o")
        .arg(&binary)
        .arg(&source)
        .status()
        .expect("run the C compiler for the layout probe");
    assert!(status.success(), "gcc layout probe failed to compile");
    let output = Command::new(&binary).output().expect("run the layout probe");
    assert!(output.status.success(), "gcc layout probe failed");
    let stdout = String::from_utf8(output.stdout).unwrap();
    let lines: Vec<_> = stdout.lines().collect();
    assert_eq!(expected.len(), lines.len(), "layout probe line count");
    expected
        .into_iter()
        .zip(lines)
        .map(|((keyword, aggregate, rust_field), line)| {
            let mut numbers = line.split(' ').map(|n| n.parse::<usize>().unwrap());
            GccField {
                keyword,
                aggregate,
                rust_field,
                offset: numbers.next().unwrap(),
                size: numbers.next().unwrap(),
            }
        })
        .collect()
}

fn render_gcc_layout(fields: &[GccField]) -> String {
    let mut out = String::from(
        "/// Layout reported by gcc with the meson flags: (aggregate, field or\n/// empty for the aggregate itself, \
         offset or size, size or align).\n",
    );
    out.push_str("pub const GCC_LAYOUT: &[(&str, &str, usize, usize)] = &[\n");
    for field in fields {
        let name = field.rust_field.as_deref().unwrap_or("");
        writeln!(
            out,
            "    ({:?}, {:?}, {}, {}), // {}",
            field.aggregate, name, field.offset, field.size, field.keyword
        )
        .unwrap();
    }
    out.push_str("];\n");
    out
}

fn render_rust_layout(fields: &[GccField]) -> String {
    let mut out = String::from(
        "/// Layout of the same aggregates and fields as the Rust bindings see it.\n#[allow(unused_unsafe)]\npub const RUST_LAYOUT: &[(&str, &str, usize, usize)] = &[\n",
    );
    for field in fields {
        let ty = &field.aggregate;
        match &field.rust_field {
            None => writeln!(
                out,
                "    ({ty:?}, \"\", ::core::mem::size_of::<crate::bindings::{ty}>(), \
                 ::core::mem::align_of::<crate::bindings::{ty}>()),"
            )
            .unwrap(),
            Some(name) => writeln!(
                out,
                "    ({ty:?}, {name:?}, ::core::mem::offset_of!(crate::bindings::{ty}, {name}), {{ let probe = \
                 ::core::mem::MaybeUninit::<crate::bindings::{ty}>::uninit(); \
                 crate::layout::size_of_pointee(unsafe {{ &raw const (*probe.as_ptr()).{name} }}) }}),"
            )
            .unwrap(),
        }
    }
    out.push_str("];\n");
    out
}

/// Builds and runs the C fixture generator: C-built LPM images the Miri
/// tests replay, since Miri itself cannot run C.
fn generate_fixtures(flags: &CFlags, shim_dir: &Path, out_dir: &Path) {
    let binary = out_dir.join("fixture_gen");
    let status = Command::new(&flags.compiler)
        .args(flags.header_args())
        .arg(format!("-I{}", shim_dir.display()))
        .arg("-O2")
        .arg("-o")
        .arg(&binary)
        .arg(shim_dir.join("fixture_gen.c"))
        .arg(shim_dir.join("test_shim.c"))
        .status()
        .expect("run the C compiler for the fixture generator");
    assert!(status.success(), "fixture generator failed to compile");
    for (key_size, name) in [(4, "lpm4.bin"), (16, "lpm6.bin")] {
        let status = Command::new(&binary)
            .arg(key_size.to_string())
            .arg(out_dir.join(name))
            .status()
            .expect("run the fixture generator");
        assert!(status.success(), "fixture generator failed for key size {key_size}");
    }
    println!("cargo:rerun-if-changed={}", shim_dir.join("fixture_gen.c").display());
}

fn compile_shim(flags: &CFlags, root: &Path, shim_dir: &Path) {
    // The dataplane feature links the C packet helpers into module objects;
    // the control-plane feature adds the allocation and LPM wrappers. A
    // control-plane-only build leaves the packet helpers out, so a process
    // that also links the C packet library sees no duplicate definitions.
    let dp = env::var_os("CARGO_FEATURE_DP").is_some();
    let cp = env::var_os("CARGO_FEATURE_CP").is_some();
    let mut build = cc::Build::new();
    build
        .compiler(&flags.compiler)
        .file(shim_dir.join("test_shim.c"))
        .include(shim_dir)
        .opt_level(2)
        .warnings(false)
        // Linked into module shared objects; nothing here is part of their
        // exported interface.
        .flag("-fvisibility=hidden");
    if dp {
        build
            .file(root.join("lib/dataplane/packet/decap.c"))
            .file(root.join("lib/dataplane/packet/packet.c"));
    }
    if cp {
        build.file(shim_dir.join("cp_shim.c"));
    }
    for include in &flags.includes {
        build.include(include);
    }
    for define in &flags.defines {
        let define = define.trim_start_matches("-D");
        match define.split_once('=') {
            Some((name, value)) => build.define(name, value),
            None => build.define(define, None),
        };
    }
    for forced in &flags.forced_includes {
        build.flag("-include").flag(forced);
    }
    for machine in &flags.machine {
        build.flag(machine);
    }
    for file in ["test_shim.c", "test_shim.h", "cp_shim.c", "cp_shim.h", "bindings.h"] {
        println!("cargo:rerun-if-changed={}", shim_dir.join(file).display());
    }
    for file in ["lib/dataplane/packet/decap.c", "lib/dataplane/packet/packet.c"] {
        println!("cargo:rerun-if-changed={}", root.join(file).display());
    }
    build.compile("yanet_sys_shim");
}
