use clap::{Parser, ValueEnum};
use clap_complete::engine::ArgValueCandidates;

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List configs.
    List,
    /// Show a config.
    Show(ShowConfigCmd),
    /// Create or replace a config.
    Set(SetConfigCmd),
    /// Delete a config.
    Delete(DeleteCmd),
    /// Read the packet dump stream of a config.
    Read(ReadCmd),
}

impl ModeCmd {
    pub(crate) fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Set(..) => "set",
            Self::Delete(..) => "delete",
            Self::Read(..) => "read",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Pdump config name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Pdump config name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct SetConfigCmd {
    /// Pdump config name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Filter represents a pcap-style filter expression.
    #[arg(long)]
    pub filter: Option<String>,

    /// Determine the packet list to capture packets from.
    #[command(flatten)]
    pub mode: Option<crate::dump_mode::Mode>,

    /// Snaplen is the maximum packet length to capture.
    #[arg(long = "snaplen", short_alias = 's')]
    pub snaplen: Option<u32>,

    /// Name of the pre-existing ring object to capture into. Required when
    /// the config is created; omit to keep the currently bound ring on an
    /// update.
    #[arg(long = "ring-name", add = ArgValueCandidates::new(crate::ring_candidates))]
    pub ring_name: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct ReadCmd {
    /// Pdump config name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Packet-dump encoding for the captured stream.
    ///
    /// Distinct from the global `--format`, which controls how CLI messages
    /// (errors, success, structured data) are rendered.
    ///
    /// Timestamp origin differs by encoding. `text` and `pretty` render an
    /// offset from the first record received, so that record itself reads
    /// zero, and the offset is displayed in a clock-shaped form despite
    /// being relative. `pcap` and `pcap-ng` carry the absolute dataplane
    /// clock value instead.
    ///
    /// Timestamps resolve to a worker's polling round rather than to an
    /// individual packet, so every record captured in one round shares a
    /// single value. Repeated identical timestamps, including several
    /// leading zeros in the relative forms, are expected.
    #[arg(long, short_alias = 'f', value_enum, default_value_t = DumpOutputFormat::Text)]
    pub dump_format: DumpOutputFormat,

    /// Dump output destination.
    #[arg(long, short = 'o')]
    pub output: Option<String>,

    /// The number of packets to capture before exiting.
    #[arg(long)]
    pub num: Option<u64>,
}

/// Dump Output format options.
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum DumpOutputFormat {
    /// Simple one-line human-readable output of the packet metadata and
    /// content.
    Text,
    /// Pretty multi-line human-readable output of the packet metadata and
    /// content.
    Pretty,
    /// PCAP Capture File Format.
    Pcap,
    /// PCAP Next Generation (pcapng) Capture File Format.
    PcapNg,
}
