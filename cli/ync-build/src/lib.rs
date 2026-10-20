//! Build-script helper for the yanet CLI crates: one protobuf build with the
//! settings and the client generator every crate shares.

use core::error::Error;
use std::path::{Path, PathBuf};

use prost_build::Config;
pub use tonic_prost_build::Builder;

use crate::generator::ClientGenerator;

mod generator;

/// A pending client build of `protos`, paths relative to `root`, the
/// repository root as seen from the crate.
pub struct Build {
    root: PathBuf,
    extra_includes: Vec<PathBuf>,
    protos: Vec<PathBuf>,
    builder: Builder,
    runtime: &'static str,
}

/// Starts a client build with shared packages mapped to their crates and no
/// rerun-if-changed noise.
///
/// Each service gets a client over the CLI gRPC runtime and a server module
/// holding only its name, so CLIs keep importing the service-name constant
/// from there.
pub fn client(root: impl AsRef<Path>, protos: &[&str]) -> Build {
    Build {
        root: root.as_ref().to_path_buf(),
        extra_includes: Vec::new(),
        protos: protos.iter().map(PathBuf::from).collect(),
        builder: shared_configure(),
        runtime: "::ync::rpc",
    }
}

/// Starts a client build whose protos also resolve through extra include
/// directories beyond `root` — for a module built outside the checkout,
/// whose proto lives in the module tree while its imports live in the
/// checkout's.
pub fn client_includes(root: impl AsRef<Path>, extra_includes: &[impl AsRef<Path>], protos: &[&str]) -> Build {
    Build {
        root: root.as_ref().to_path_buf(),
        extra_includes: extra_includes.iter().map(|p| p.as_ref().to_path_buf()).collect(),
        protos: protos.iter().map(PathBuf::from).collect(),
        builder: shared_configure(),
        runtime: "::ync::rpc",
    }
}

fn shared_configure() -> Builder {
    tonic_prost_build::configure()
        .emit_rerun_if_changed(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
}

impl Build {
    /// Names the path of the CLI gRPC runtime the clients call, `::ync::rpc`
    /// by default; a crate below `ync` names the runtime crate itself.
    pub fn runtime(self, runtime: &'static str) -> Self {
        Self { runtime, ..self }
    }

    /// Derives `serde::Serialize` on every message.
    pub fn serialize(self) -> Self {
        self.with(|builder| builder.message_attribute(".", "#[derive(serde::Serialize)]"))
    }

    /// Applies the crate's own settings to the underlying tonic builder.
    pub fn with(mut self, configure: impl FnOnce(Builder) -> Builder) -> Self {
        self.builder = configure(self.builder);

        self
    }

    /// Compiles the protos, rerunning the build script when one of them
    /// changes. A proto they import is not watched.
    pub fn compile(self) -> Result<(), Box<dyn Error>> {
        let protos: Vec<PathBuf> = self.protos.iter().map(|proto| self.root.join(proto)).collect();

        for proto in &protos {
            println!("cargo:rerun-if-changed={}", proto.display());
        }

        let mut includes = vec![self.root.clone()];
        includes.extend(self.extra_includes.iter().cloned());

        let mut config = Config::new();
        config.service_generator(Box::new(ClientGenerator::new(self.runtime)));

        self.builder
            .build_client(false)
            .build_server(false)
            .compile_with_config(config, &protos, &includes)?;

        Ok(())
    }
}
