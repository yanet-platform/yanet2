use std::path::PathBuf;

use clap::Parser;

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Delete(DeleteCmd),
    Update(UpdateCmd),
    Show(ShowCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Ruleset file name.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL module name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}
