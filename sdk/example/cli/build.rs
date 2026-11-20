use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    // The proto include root is the yanet2 checkout this module is built
    // against: YANET_ROOT overrides it for a module copied outside the
    // checkout (the Makefile exports it); the default resolves in-tree.
    // The module's own tree is a second include dir, so the proto is
    // named from it and a copied template keeps building after a rename.
    let root = std::env::var("YANET_ROOT").unwrap_or_else(|_| "../../../".to_string());
    let manifest = std::env::var("CARGO_MANIFEST_DIR").unwrap();
    let module_root = format!("{manifest}/..");
    // Absolute, so the join onto the include root in ync-build keeps it
    // intact; protoc resolves it through the module include dir.
    let proto = format!("{module_root}/controlplane/examplepb/v1/example.proto");
    ync_build::client_includes(root, &[module_root], &[proto.as_str()])
        .serialize()
        .compile()
}
