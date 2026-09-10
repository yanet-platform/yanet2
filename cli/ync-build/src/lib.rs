//! Build-script helper for the yanet CLI crates: one client-only tonic
//! build with the settings every crate shares.

use core::error::Error;
use std::path::{Path, PathBuf};

pub use tonic_prost_build::Builder;

/// A pending client build of `protos`, paths relative to `root`, the
/// repository root as seen from the crate.
pub struct Build {
    root: PathBuf,
    protos: Vec<PathBuf>,
    builder: Builder,
}

/// Starts a client-only build with the shared packages mapped to their
/// crates and no rerun-if-changed noise.
pub fn client(root: impl AsRef<Path>, protos: &[&str]) -> Build {
    let builder = tonic_prost_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".common.filterpb.v1", "::filterpb::pb");

    Build {
        root: root.as_ref().to_path_buf(),
        protos: protos.iter().map(PathBuf::from).collect(),
        builder,
    }
}

impl Build {
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

        self.builder.compile_protos(&protos, &[self.root])?;

        Ok(())
    }
}
