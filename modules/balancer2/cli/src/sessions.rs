use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

use crate::FilterFlags;

#[derive(Debug, Clone, Parser)]
pub struct SessionsCmd {
    #[clap(subcommand)]
    pub mode: SessionsMode,
}

#[derive(Debug, Clone, Parser)]
pub enum SessionsMode {
    /// List all sessions states.
    List,
    /// Stream active sessions for the specified sessions state.
    Show(SessionsShowCmd),
    /// Create or resize a sessions state.
    Update(SessionsUpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct SessionsShowCmd {
    /// Sessions state name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(crate::sessions_candidates))]
    pub name: String,

    #[command(flatten)]
    pub filter: FilterFlags,
}

#[derive(Debug, Clone, Parser)]
pub struct SessionsUpdateCmd {
    /// Sessions state name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(crate::sessions_candidates))]
    pub name: String,
    /// Capacity (number of session entries).
    #[arg(long, short_alias = 'c')]
    pub capacity: u64,
}
