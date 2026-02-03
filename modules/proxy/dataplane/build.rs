use std::env;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-changed=wrapper.h");
    println!("cargo:rerun-if-changed=config.h");

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

    let dpdk_root = env::var("DPDK_ROOT")
        .unwrap_or_else(|_| format!("{}/subprojects/dpdk", project_root.display()));

    let build_dir = env::var("MESON_BUILD_ROOT")
        .unwrap_or_else(|_| format!("{}/build", project_root.display()));

    let bindings = bindgen::Builder::default()
        .header("wrapper.h")
        .clang_arg("-march=corei7")
        .clang_arg(format!("-I{}", project_root.display()))
        .clang_arg(format!("-I{}/lib", project_root.display()))
        .clang_arg(format!("-I{}/subprojects/dpdk", build_dir))
        .clang_arg(format!("-I{}/lib/eal/include", dpdk_root))
        .clang_arg(format!("-I{}/lib/eal/x86/include", dpdk_root))
        .clang_arg(format!("-I{}/lib/eal/linux/include", dpdk_root))
        .clang_arg(format!("-I{}/lib/log", dpdk_root))
        .clang_arg(format!("-I{}/lib/mbuf", dpdk_root))
        .clang_arg(format!("-I{}/lib/mempool", dpdk_root))
        .clang_arg(format!("-I{}/lib/ring", dpdk_root))
        .clang_arg(format!("-I{}/lib/net", dpdk_root))
        .clang_arg(format!("-I{}/lib/ethdev", dpdk_root))
        .clang_arg(format!("-I{}/config", dpdk_root))
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

    cc::Build::new()
        .file(out_path.join("static_fns.c"))
        .include(&manifest_dir)
        .include(&project_root)
        .include(format!("{}/lib", project_root.display()))
        .include(format!("{}/subprojects/dpdk", build_dir))
        .include(format!("{}/lib/eal/include", dpdk_root))
        .include(format!("{}/lib/eal/include/generic", dpdk_root))
        .include(format!("{}/lib/eal/x86/include", dpdk_root))
        .include(format!("{}/lib/eal/linux/include", dpdk_root))
        .include(format!("{}/lib/log", dpdk_root))
        .include(format!("{}/lib/mbuf", dpdk_root))
        .include(format!("{}/lib/mempool", dpdk_root))
        .include(format!("{}/lib/ring", dpdk_root))
        .include(format!("{}/lib/net", dpdk_root))
        .include(format!("{}/lib/ethdev", dpdk_root))
        .include(format!("{}/config", dpdk_root))
        .flag("-march=corei7")
        .compile("static_fns");

    println!("cargo::rustc-link-search={}", out_path.to_str().unwrap());
    println!("cargo:rustc-link-lib=static=static_fns");
}
