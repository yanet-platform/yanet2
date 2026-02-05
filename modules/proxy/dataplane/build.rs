use std::env;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-changed=wrapper.h");
    println!("cargo:rerun-if-changed=config.h");

    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let project_root = manifest_dir.join("../../../");

    let out_path = PathBuf::from(env::var("OUT_DIR").unwrap());

    let dpdk_root = env::var("DPDK_ROOT")
        .map(PathBuf::from)
        .unwrap_or_else(|_| project_root.join("subprojects/dpdk"));

    let build_root = env::var("MESON_BUILD_ROOT")
        .map(PathBuf::from)
        .unwrap_or_else(|_| project_root.join("build"));
    let dpdk_build_root = build_root.join("subprojects/dpdk");

    let bindings = bindgen::Builder::default()
        .header("wrapper.h")
        .clang_arg("-march=corei7")
        .clang_arg(format!("-I{}", project_root.display()))
        .clang_arg(format!("-I{}/lib", project_root.display()))
        .clang_arg(format!("-I{}/subprojects/dpdk", build_root.display()))
        .clang_arg(format!("-I{}/subprojects/dpdk/config", build_root.display()))
        .clang_arg(format!("-I{}/lib/eal/include", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/eal/x86/include", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/eal/linux/include", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/log", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/mbuf", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/mempool", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/ring", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/net", dpdk_root.display()))
        .clang_arg(format!("-I{}/lib/ethdev", dpdk_root.display()))
        .clang_arg(format!("-I{}/config", dpdk_root.display()))
        .wrap_static_fns(true)
        .wrap_static_fns_path(out_path.join("static_fns.c"))
        .allowlist_type("module")
        .allowlist_type("module_handler")
        .allowlist_type("module_ectx")
        .allowlist_type("packet_front")
        .allowlist_type("packet")
        .allowlist_type("packet_list")
        .allowlist_type("packet_header")
        .allowlist_type("network_header")
        .allowlist_type("transport_header")
        .allowlist_type("dp_worker")
        .allowlist_type("cp_module")
        .allowlist_type("ipv4_prefix")
        .allowlist_type("proxy_config")
        .allowlist_type("proxy_module_config")
        .allowlist_function("packet_.*")
        .allowlist_var("MODULE_NAME_LEN")
        .allowlist_type("rte_mbuf")
        .allowlist_type("rte_ipv4_hdr")
        .allowlist_type("rte_tcp_hdr")
        .allowlist_type("rte_ether_hdr")
        .allowlist_function("rte_ipv4_cksum")
        .allowlist_function("rte_ipv4_hdr_len")
        .allowlist_function("rte_raw_cksum")
        .allowlist_var("RTE_ETHER_.*")
        .allowlist_var("RTE_TCP_.*")
        .allowlist_var("IPPROTO_UDP")
        .derive_default(true)
        .derive_debug(true)
        .generate()
        .expect("Unable to generate bindings");

    bindings
        .write_to_file(out_path.join("bindings.rs"))
        .expect("Couldn't write bindings!");

    let includes = [
        &dpdk_root,
        &dpdk_build_root,
        &dpdk_root.join("config"),
        &dpdk_root.join("lib/eal/include"),
        &dpdk_root.join("lib/eal/linux/include"),
        &dpdk_root.join("lib/eal/x86/include"),
        &dpdk_root.join("lib/eal/common"),
        &dpdk_root.join("lib/eal"),
        &dpdk_root.join("lib/log"),
        &dpdk_root.join("lib/mbuf"),
        &dpdk_root.join("lib/mempool"),
        &dpdk_root.join("lib/ring"),
        &dpdk_root.join("lib/net"),
        &dpdk_root.join("lib/ethdev"),
        &manifest_dir,
        &project_root,
        &project_root.join("lib"),
    ];

    cc::Build::new()
        .includes(includes)
        // fixme
        .flag("-march=corei7")
        .file(out_path.join("static_fns.c"))
        .compile("static_fns");

    println!("cargo::rustc-link-search={}", out_path.to_str().unwrap());
    println!("cargo:rustc-link-lib=static=static_fns");
}
