use core::str::FromStr;

use bytesize::ByteSize;
use clap::{Parser, ValueEnum};

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    Set(SetConfigCmd),
    Delete(DeleteCmd),
    Read(ReadCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Pdump config name to delete.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = ConfigOutputFormat::Tree)]
    pub format: ConfigOutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct SetConfigCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,

    /// Filter represents a pcap-style filter expression
    #[arg(long, short)]
    pub filter: Option<String>,

    /// Determine the packet list to capture packets from.
    #[command(flatten)]
    pub mode: Option<crate::dump_mode::Mode>,

    /// Snaplen is the maximum packet length to capture.
    #[arg(long = "snaplen", short = 's')]
    pub snaplen: Option<u32>,

    /// Per-worker ring buffer size
    #[arg(long = "ring-size")]
    pub ring_size: Option<RingBufferSize>,
}

#[derive(Debug, Clone, Parser)]
pub struct ReadCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,

    /// Dump output format.
    #[clap(long, short = 'f', value_enum, default_value_t = DumpOutputFormat::Text)]
    pub format: DumpOutputFormat,

    /// Dump output destination.
    #[clap(long, short = 'o')]
    pub output: Option<String>,

    /// The number of packets to capture before exiting.
    #[arg(long, short)]
    pub num: Option<u64>,
}

/// Ring buffer size.
#[derive(Debug, Clone, Copy)]
pub struct RingBufferSize(u32);

impl RingBufferSize {
    /// Minimum ring buffer size.
    const MIN: ByteSize = ByteSize::mib(1);
    /// Maximum ring buffer size.
    const MAX: ByteSize = ByteSize::mib(64);

    /// Get the underlying byte size.
    #[inline]
    pub const fn get(self) -> u32 {
        let Self(v) = self;
        v
    }
}

impl FromStr for RingBufferSize {
    type Err = String;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s.parse::<ByteSize>()? {
            v if v < Self::MIN => Err(format!("less than minimum of {}", Self::MIN)),
            v if v > Self::MAX => Err(format!("exceeds maximum of {}", Self::MAX)),
            v if !v.as_u64().is_power_of_two() => Err(format!("value is not a power of two: {}", v.as_u64())),
            v => {
                // NOTE: truncation is impossible because of the above checks.
                let v = v.as_u64() as u32;
                Ok(Self(v))
            }
        }
    }
}

/// Dump Output format options.
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum DumpOutputFormat {
    /// Simple one-line human-readable output of the packet metadata and content
    Text,
    /// Pretty multi-line human-readable output of the packet metadata and
    /// content
    Pretty,
    /// PCAP Capture File Format
    Pcap,
    /// PCAP Next Generation (pcapng) Capture File Format
    PcapNg,
}

/// Config output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum ConfigOutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
}
