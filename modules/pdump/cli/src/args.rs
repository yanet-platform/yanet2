use clap::{Args, Parser, ValueEnum};

use crate::pdumppb::DumpMode;

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Show(ShowConfigCmd),
    SetFilter(SetFilterCmd),
    SetDumpMode(SetDumpModeCmd),
    SetSnapLen(SetSnapLenCmd),
    SetRingSize(SetRingSizeCmd),
    Read(ReadCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: Option<String>,
    /// NUMA node index where changes should be applied, optionally repeated.
    #[arg(long, required = false)]
    pub numa: Vec<u32>,
    /// Output format.
    #[clap(long, value_enum, default_value_t = ConfigOutputFormat::Tree)]
    pub format: ConfigOutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct SetFilterCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// NUMA node index where changes should be applied, optionally repeated.
    #[arg(long, required = true)]
    pub numa: Vec<u32>,

    #[arg(long = "filter", short)]
    pub filter: String,
}

#[derive(Debug, Clone, Parser)]
pub struct SetDumpModeCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// NUMA node index where changes should be applied, optionally repeated.
    #[arg(long, required = true)]
    pub numa: Vec<u32>,

    /// Determine the packet list to capture packets from.
    #[command(flatten)]
    pub mode: Mode,
}

#[derive(Debug, Clone, Parser)]
pub struct SetSnapLenCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// NUMA node index where changes should be applied, optionally repeated.
    #[arg(long, required = true)]
    pub numa: Vec<u32>,

    // SnapLen is the maximum packet length to capture.
    #[arg(long = "snaplen", short = 's')]
    pub snaplen: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct ReadCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// NUMA node index where the data should be read.
    #[arg(long, required = true)]
    pub numa: Vec<u32>,

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

#[derive(Debug, Clone, Parser)]
pub struct SetRingSizeCmd {
    /// Pdump config name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// NUMA node index where changes should be applied, optionally repeated.
    #[arg(long, required = true)]
    pub numa: Vec<u32>,

    /// Per-worker ring buffer size
    #[arg(long = "size", short = 's', value_parser=ring_buffer_size_range)]
    pub ring_size: u32,
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

#[derive(Args, Debug, Clone, Copy)]
#[group(required = false, multiple = false)]
pub struct Mode {
    /// Capture input packets
    #[arg(long)]
    pub input: bool,

    /// Capture dropped packets
    #[arg(long)]
    pub drops: bool,

    /// Capture both input and dropped packets.
    #[arg(long)]
    pub both: bool,
}

impl From<Mode> for i32 {
    fn from(val: Mode) -> Self {
        let (input, drops, both) = (val.input, val.drops, val.both);
        match (input, drops, both) {
            // default case - dump the input packet list
            (false, false, false) => DumpMode::PdumpDumpInput.into(),
            (true, false, false) => DumpMode::PdumpDumpInput.into(),
            (false, true, false) => DumpMode::PdumpDumpDrops.into(),
            _ => DumpMode::PdumpDumpBoth.into(),
        }
    }
}
