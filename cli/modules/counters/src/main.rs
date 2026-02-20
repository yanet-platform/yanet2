//! CLI for YANET "counters" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    counters_service_client::CountersServiceClient, ChainCountersRequest, DeviceCountersRequest,
    FunctionCountersRequest, ModuleCountersRequest, PerfCountersRequest, PipelineCountersRequest,
};
use colored::Colorize;
use tonic::codec::CompressionEncoding;
use ync::{client::{ConnectionArgs, LayeredChannel}, logging};

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("ynpb");
}

/// Counters module - displays counters information.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show device counters.
    Device(DeviceCmd),
    /// Show pipeline counters.
    Pipeline(PipelineCmd),
    /// Show pipeline counters.
    Function(FunctionCmd),
    /// Show pipeline counters.
    Chain(ChainCmd),
    /// Show counters of module assigned to a pipeline.
    Module(ModuleCmd),
    /// Show performance counters for a module.
    Perf(PerfCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeviceCmd {
    #[arg(long)]
    pub device_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct PipelineCmd {
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct FunctionCmd {
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ChainCmd {
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
    #[arg(long)]
    pub chain_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ModuleCmd {
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
    #[arg(long)]
    pub chain_name: String,
    #[arg(long)]
    pub module_type: String,
    #[arg(long)]
    pub module_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct PerfCmd {
    /// Device name
    #[arg(short = 'd', long)]
    pub device: String,
    /// Pipeline name
    #[arg(short = 'p', long)]
    pub pipeline: String,
    /// Function name
    #[arg(short = 'f', long)]
    pub function: String,
    /// Chain name
    #[arg(short = 'c', long)]
    pub chain: String,
    /// Module in format module_type:module_name
    #[arg(short = 'm', long)]
    pub module: String,
    /// Output raw JSON instead of formatted histogram
    #[arg(long)]
    pub json: bool,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = CountersService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Device(cmd) => service.show_device(cmd.device_name).await?,
        ModeCmd::Pipeline(cmd) => service.show_pipeline(cmd.device_name, cmd.pipeline_name).await?,
        ModeCmd::Function(cmd) => {
            service
                .show_function(cmd.device_name, cmd.pipeline_name, cmd.function_name)
                .await?
        }
        ModeCmd::Chain(cmd) => {
            service
                .show_chain(cmd.device_name, cmd.pipeline_name, cmd.function_name, cmd.chain_name)
                .await?
        }
        ModeCmd::Module(cmd) => {
            service
                .show_module(
                    cmd.device_name,
                    cmd.pipeline_name,
                    cmd.function_name,
                    cmd.chain_name,
                    cmd.module_type,
                    cmd.module_name,
                )
                .await?
        }
        ModeCmd::Perf(cmd) => {
            // Parse module format: module_type:module_name
            let parts: Vec<&str> = cmd.module.split(':').collect();
            if parts.len() != 2 {
                return Err(format!(
                    "Invalid module format '{}'. Expected format: module_type:module_name",
                    cmd.module
                )
                .into());
            }
            let module_type = parts[0].to_string();
            let module_name = parts[1].to_string();
            
            service
                .show_perf(
                    cmd.device,
                    cmd.pipeline,
                    cmd.function,
                    cmd.chain,
                    module_type,
                    module_name,
                    cmd.json,
                )
                .await?
        }
    }

    Ok(())
}

pub struct CountersService {
    client: CountersServiceClient<LayeredChannel>,
}

impl CountersService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = CountersServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn show_device(&mut self, device_name: String) -> Result<(), Box<dyn Error>> {
        let request = DeviceCountersRequest { device: device_name };
        let response = self.client.device(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_pipeline(&mut self, device_name: String, pipeline_name: String) -> Result<(), Box<dyn Error>> {
        let request = PipelineCountersRequest {
            device: device_name,
            pipeline: pipeline_name,
        };
        let response = self.client.pipeline(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_function(
        &mut self,
        device_name: String,
        pipeline_name: String,
        function_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = FunctionCountersRequest {
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
        };
        let response = self.client.function(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_chain(
        &mut self,
        device_name: String,
        pipeline_name: String,
        function_name: String,
        chain_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = ChainCountersRequest {
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
            chain: chain_name,
        };
        let response = self.client.chain(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_module(
        &mut self,
        device_name: String,
        pipeline_name: String,
        function_name: String,
        chain_name: String,
        module_type: String,
        module_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = ModuleCountersRequest {
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
            chain: chain_name,
            module_type,
            module_name,
            counter_query: Vec::new(),
        };
        let response = self.client.module(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_perf(
        &mut self,
        device_name: String,
        pipeline_name: String,
        function_name: String,
        chain_name: String,
        module_type: String,
        module_name: String,
        json: bool,
    ) -> Result<(), Box<dyn Error>> {
        let request = PerfCountersRequest {
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
            chain: chain_name,
            module_type,
            module_name,
        };
        let response = self.client.perf(request).await?;
        
        if json {
            println!("{}", serde_json::to_string(response.get_ref())?);
        } else {
            format_perf_counters(response.get_ref());
        }
        Ok(())
    }
}

/// Format and display performance counters with beautiful histogram output
fn format_perf_counters(response: &code::PerfCountersResponse) {
    // Header
    println!("{}", "╔══════════════════════════════════════════════════════════════════╗".bright_cyan());
    println!("{}", "║                    Performance Counters                          ║".bright_cyan());
    println!("{}", "╠══════════════════════════════════════════════════════════════════╣".bright_cyan());
    
    // Summary stats
    let tx_gb = response.tx_bytes as f64 / 1_073_741_824.0;
    let rx_gb = response.rx_bytes as f64 / 1_073_741_824.0;
    println!(
        "{}  TX: {} packets ({:.2} GB)  {}  RX: {} packets ({:.2} GB)",
        "║".bright_cyan(),
        format_number(response.tx).bright_green(),
        tx_gb,
        "│".bright_black(),
        format_number(response.rx).bright_green(),
        rx_gb
    );
    println!("{}", "╚══════════════════════════════════════════════════════════════════╝".bright_cyan());
    println!();
    
    // Process each batch size counter
    for counter in &response.counters {
        format_batch_counter(counter);
        println!();
    }
}

/// Format a single batch size counter with histogram
fn format_batch_counter(counter: &code::PerfCounter) {
    let batch_range = match counter.min_batch_size {
        1 => "1 packet".to_string(),
        2 => "2-3 packets".to_string(),
        4 => "4-7 packets".to_string(),
        8 => "8-15 packets".to_string(),
        16 => "16-31 packets".to_string(),
        32 => "32+ packets".to_string(),
        _ => format!("{}+ packets", counter.min_batch_size),
    };
    
    println!("{} Batch Size: {} {}", "┌─".bright_black(), batch_range.bright_yellow(), "─".repeat(50).bright_black());
    
    // Calculate statistics
    let total_batches: u64 = counter.latencies.iter().map(|l| l.batches).sum();
    let total_packets = counter.packets;
    
    // Average latency per packet and per batch
    let avg_latency_per_packet = if total_packets > 0 {
        counter.summary_latency / total_packets
    } else {
        0
    };
    let avg_latency_per_batch = if total_batches > 0 {
        counter.summary_latency / total_batches
    } else {
        0
    };
    
    // Line 1: Total packets and batches
    println!(
        "{}  Total: {} packets ({} batches)",
        "│".bright_black(),
        format_number(total_packets).bright_white(),
        format_number(total_batches).bright_white()
    );
    
    // Line 2: Avg latency per packet/batch and total latency
    println!(
        "{}  Avg Latency: {}/pkt ({}/batch) {} Total: {}",
        "│".bright_black(),
        format_latency(avg_latency_per_packet).bright_cyan(),
        format_latency(avg_latency_per_batch).bright_cyan(),
        "│".bright_black(),
        format_latency(counter.summary_latency).bright_cyan()
    );
    
    if !counter.latencies.is_empty() {
        println!("{}", "├────────────────────────────────────────────────────────────────────┤".bright_black());
        
        // Find max batches for scaling
        let max_batches = counter.latencies.iter().map(|l| l.batches).max().unwrap_or(1);
        
        // Display histogram
        for (i, latency) in counter.latencies.iter().enumerate() {
            let next_latency = counter.latencies.get(i + 1).map(|l| l.min_latency);
            let range_str = format_latency_range(latency.min_latency, next_latency);
            let percentage = if total_batches > 0 {
                (latency.batches as f64 / total_batches as f64) * 100.0
            } else {
                0.0
            };
            
            // Calculate bar length (max 40 characters)
            let bar_length = ((latency.batches as f64 / max_batches as f64) * 40.0) as usize;
            let bar = "█".repeat(bar_length);
            
            println!(
                "{} {:>12} {} {:<40} {} {} ({:.1}%)",
                "│".bright_black(),
                range_str.bright_white(),
                "│".bright_black(),
                bar.bright_green(),
                "│".bright_black(),
                format_number(latency.batches).bright_white(),
                percentage
            );
        }
        
        // Calculate percentiles
        if total_batches > 0 {
            let (p50, p90, p99, max) = calculate_percentiles(&counter.latencies, total_batches);
            println!("{}", "├────────────────────────────────────────────────────────────────────┤".bright_black());
            println!(
                "{} p50: {} {} p90: {} {} p99: {} {} max: {}",
                "│".bright_black(),
                format_latency(p50).bright_cyan(),
                "│".bright_black(),
                format_latency(p90).bright_yellow(),
                "│".bright_black(),
                format_latency(p99).bright_red(),
                "│".bright_black(),
                format_latency(max).bright_magenta()
            );
        }
    }
    
    println!("{}", "└────────────────────────────────────────────────────────────────────┘".bright_black());
}

/// Calculate percentiles from histogram data
fn calculate_percentiles(latencies: &[code::LatencyRangeCounter], total_batches: u64) -> (u64, u64, u64, u64) {
    let p50_target = (total_batches as f64 * 0.50) as u64;
    let p90_target = (total_batches as f64 * 0.90) as u64;
    let p99_target = (total_batches as f64 * 0.99) as u64;
    
    let mut cumulative = 0u64;
    let mut p50 = 0u64;
    let mut p90 = 0u64;
    let mut p99 = 0u64;
    let mut max = 0u64;
    
    for latency in latencies {
        cumulative += latency.batches;
        max = latency.min_latency as u64;
        
        if p50 == 0 && cumulative >= p50_target {
            p50 = latency.min_latency as u64;
        }
        if p90 == 0 && cumulative >= p90_target {
            p90 = latency.min_latency as u64;
        }
        if p99 == 0 && cumulative >= p99_target {
            p99 = latency.min_latency as u64;
        }
    }
    
    (p50, p90, p99, max)
}

/// Format a latency range string
fn format_latency_range(min: u32, next: Option<u32>) -> String {
    if let Some(next_val) = next {
        format!("{}-{}", format_latency(min as u64), format_latency(next_val as u64))
    } else {
        format!("{}+", format_latency(min as u64))
    }
}

/// Format latency in human-readable form (ns, µs, ms, s) without space
fn format_latency(ns: u64) -> String {
    if ns < 1_000 {
        format!("{}ns", ns)
    } else if ns < 1_000_000 {
        format!("{:.1}µs", ns as f64 / 1_000.0)
    } else if ns < 1_000_000_000 {
        format!("{:.2}ms", ns as f64 / 1_000_000.0)
    } else {
        format!("{:.3}s", ns as f64 / 1_000_000_000.0)
    }
}

/// Format large numbers with thousand separators
fn format_number(n: u64) -> String {
    let s = n.to_string();
    let mut result = String::new();
    let mut count = 0;
    
    for c in s.chars().rev() {
        if count > 0 && count % 3 == 0 {
            result.push(',');
        }
        result.push(c);
        count += 1;
    }
    
    result.chars().rev().collect()
}
