use std::path::PathBuf;

use clap::Parser;

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all ACL configs
    List,
    /// Delete an ACL config
    Delete(DeleteCmd),
    /// Upload a new ACL config from a YAML file
    Update(UpdateCmd),
    /// Show ACL config rules
    Show(ShowCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// ACL config name
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// ACL config name
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Path to the ruleset YAML file
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL config name
    #[arg(long = "cfg", short)]
    pub config_name: String,
}
