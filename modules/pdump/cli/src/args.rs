use clap::{Parser, ValueEnum};

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    Set(SetConfigCmd),
    Read(ReadCmd),
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
    #[arg(long = "ring-size", value_parser=ring_buffer_size_range)]
    pub ring_size: Option<u32>,
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

fn ring_buffer_size_range(s: &str) -> Result<u64, String> {
    let val = s.parse::<bytesize::ByteSize>()?;
    let min = bytesize::ByteSize::mib(1);
    let max = bytesize::ByteSize::mib(64);
    if val > max {
        Err(format!("exceeds maximum of {max}"))
    } else if val < min {
        Err(format!("less than minimum of {min}"))
    } else {
        let val = val.as_u64();
        if !val.is_power_of_two() {
            return Err(format!("value is not a power of two: {val}"));
        }
        Ok(val)
    }
}

/// Dump Output format options.
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum DumpOutputFormat {
    /// Simple one-line human-readable output of the packet metadata and content
    Text,
    /// Pretty multi-line human-readable output of the packet metadata and content
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
