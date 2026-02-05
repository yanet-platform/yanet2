use std::env;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-changed=wrapper.h");

    let manifest_dir = env::var("CARGO_MANIFEST_DIR").unwrap();
    let project_root = PathBuf::from(&manifest_dir)
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .to_path_buf();

    let out_path = PathBuf::from(env::var("OUT_DIR").unwrap());

    let bindings = bindgen::Builder::default()
        .header("wrapper.h")
        .clang_arg("-march=corei7")
        .clang_arg(format!("-I{}", project_root.display()))
        .clang_arg(format!("-I{}/lib", project_root.display()))
        .wrap_static_fns(true)
        .wrap_static_fns_path(out_path.join("static_fns.c"))
        .allowlist_type("module")
        .allowlist_type("module_handler")
        .allowlist_type("module_ectx")
        .allowlist_type("dp_worker")
        .allowlist_type("cp_module")
        .allowlist_function("cp_module_init")
        .allowlist_function("memory_balloc")
        .allowlist_function("memory_bfree")
        .allowlist_function("agent_delete_module")
        .allowlist_var("MODULE_NAME_LEN")
        .derive_default(true)
        .derive_debug(true)
        .generate()
        .expect("Unable to generate bindings");

    bindings
        .write_to_file(out_path.join("bindings.rs"))
        .expect("Couldn't write bindings!");

    cc::Build::new()
        .file(out_path.join("static_fns.c"))
        .include(&manifest_dir)
        .include(&project_root)
        .include(format!("{}/lib", project_root.display()))
        .flag("-march=corei7")
        .compile("static_fns");

    println!("cargo::rustc-link-search={}", out_path.to_str().unwrap());
    println!("cargo:rustc-link-lib=static=static_fns");
}
