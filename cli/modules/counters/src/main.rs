//! CLI for YANET "counters" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};
use ynpb::pb::{
    counters_service_client::CountersServiceClient, ChainCountersRequest, DeviceCountersRequest,
    FunctionCountersRequest, LatencyRangeCounter, ModuleCountersRequest, PerfCounter, PerfCountersRequest,
    PerfCountersResponse, PipelineCountersRequest,
};

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

            let request = PerfCountersRequest {
                device: cmd.device,
                pipeline: cmd.pipeline,
                function: cmd.function,
                chain: cmd.chain,
                module_type,
                module_name,
            };

            service.show_perf(request, cmd.json).await?
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

    pub async fn show_perf(&mut self, request: PerfCountersRequest, json: bool) -> Result<(), Box<dyn Error>> {
        let response = self.client.perf(request).await?;

        if json {
            println!("{}", serde_json::to_string(response.get_ref())?);
        } else {
            format_perf_counters(response.get_ref());
        }
        Ok(())
    }
}

/// Column widths for histogram alignment across all batch counters
struct HistogramWidths {
    max_left_val_width: usize,
    max_right_val_width: usize,
    max_count_width: usize,
}

/// Calculate global column widths across all counters for consistent alignment
fn calculate_global_widths(counters: &[PerfCounter]) -> HistogramWidths {
    let mut max_left_val_width = 0usize;
    let mut max_right_val_width = 0usize;
    let mut max_count_width = 0usize;

    for counter in counters {
        for (i, latency) in counter.latencies.iter().enumerate() {
            // Left value width
            let left_width = display_width(&format_latency(latency.min_latency as u64));
            max_left_val_width = max_left_val_width.max(left_width);

            // Right value width (next latency's min_latency)
            if let Some(next) = counter.latencies.get(i + 1) {
                let right_width = display_width(&format_latency(next.min_latency as u64));
                max_right_val_width = max_right_val_width.max(right_width);
            }

            // Count width (use display_width for consistency with UTF-8)
            let count_width = display_width(&format_number(latency.batches));
            max_count_width = max_count_width.max(count_width);
        }
    }

    HistogramWidths {
        max_left_val_width,
        max_right_val_width,
        max_count_width,
    }
}

/// Format and display performance counters with beautiful histogram output
fn format_perf_counters(response: &PerfCountersResponse) {
    // Header - 74 chars inner width (76 total with borders)
    println!(
        "{}",
        "╔══════════════════════════════════════════════════════════════════════════╗".bright_cyan()
    );
    println!(
        "{}",
        "║                         Performance Counters                             ║".bright_cyan()
    );
    println!(
        "{}",
        "╠══════════════════════════════════════════════════════════════════════════╣".bright_cyan()
    );

    // Summary stats - RX first, then TX
    let rx_str = format!(
        "RX: {} packets ({})",
        format_number(response.rx),
        format_bytes(response.rx_bytes)
    );
    let tx_str = format!(
        "TX: {} packets ({})",
        format_number(response.tx),
        format_bytes(response.tx_bytes)
    );

    // Build the content and pad to exactly 74 chars
    // Format: "  RX: ... packets (...)   │   TX: ... packets (...)  "
    let rx_width = display_width(&rx_str);
    let tx_width = display_width(&tx_str);
    let separator = " │ "; // 3 chars
    let separator_width = 3;

    // Total content width without padding (used for reference)
    let _content_width = rx_width + separator_width + tx_width;

    // Distribute padding: some before RX, some between RX and separator, some after
    // TX We want the separator centered, so left_half and right_half should be
    // equal
    let left_half: usize = 37; // (74 - 0) / 2, but we want separator at position 37
    let right_half: usize = 37;

    // Left side: padding + rx_str should fill left_half chars (before separator)
    let rx_total_space = left_half.saturating_sub(1); // -1 for the space before │
    let rx_left_pad = rx_total_space.saturating_sub(rx_width) / 2;
    let rx_right_pad = rx_total_space.saturating_sub(rx_width).saturating_sub(rx_left_pad);

    // Right side: tx_str + padding should fill right_half chars (after separator)
    let tx_total_space = right_half.saturating_sub(2); // -2 for "│ " after separator
    let tx_left_pad = tx_total_space.saturating_sub(tx_width) / 2;
    let tx_right_pad = tx_total_space.saturating_sub(tx_width).saturating_sub(tx_left_pad);

    println!(
        "{}{}{}{}{}{}{}{}{}",
        "║".bright_cyan(),
        " ".repeat(rx_left_pad),
        rx_str.bright_green(),
        " ".repeat(rx_right_pad),
        separator.bright_black(),
        " ".repeat(tx_left_pad),
        tx_str.bright_green(),
        " ".repeat(tx_right_pad),
        "║".bright_cyan()
    );
    println!(
        "{}",
        "╚══════════════════════════════════════════════════════════════════════════╝".bright_cyan()
    );
    println!();

    // Calculate global column widths for consistent alignment across all histograms
    let widths = calculate_global_widths(&response.counters);

    // Process each batch size counter
    for (i, counter) in response.counters.iter().enumerate() {
        let next_min_batch = response.counters.get(i + 1).map(|c| c.min_batch_size);
        format_batch_counter(counter, next_min_batch, &widths);
        println!();
    }
}

/// Format a single batch size counter with histogram
fn format_batch_counter(counter: &PerfCounter, next_min_batch: Option<u32>, widths: &HistogramWidths) {
    // Table width: 76 chars total (74 inner + 2 for borders)
    const TABLE_WIDTH: usize = 74;

    // Calculate the batch range dynamically from next counter's min_batch_size
    let batch_range = if let Some(next) = next_min_batch {
        let max_batch_size = next - 1;
        if counter.min_batch_size == max_batch_size {
            if counter.min_batch_size == 1 {
                "1 packet".to_string()
            } else {
                format!("{} packets", counter.min_batch_size)
            }
        } else {
            format!("{}-{} packets", counter.min_batch_size, max_batch_size)
        }
    } else {
        format!("{}+ packets", counter.min_batch_size)
    };

    // Header line: "┌─ Batch Size: X packets ─────...─────┐"
    let header_text = format!(" Batch Size: {} ", batch_range);
    let dashes_needed = TABLE_WIDTH - header_text.len() - 1; // -1 for the closing ┐
    println!(
        "{}{}{}{}",
        "┌─".bright_black(),
        header_text.bright_yellow(),
        "─".repeat(dashes_needed).bright_black(),
        "┐".bright_black()
    );

    // Calculate statistics
    let total_batches: u64 = counter.latencies.iter().map(|l| l.batches).sum();
    let total_packets = counter.packets;
    let total_bytes = counter.bytes;

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

    // Line 1: Total batches, packets, and bytes
    let total_content = format!(
        "  Total: {} batches ({} packets, {})",
        format_number(total_batches),
        format_number(total_packets),
        format_bytes(total_bytes)
    );
    let padding1 = TABLE_WIDTH.saturating_sub(display_width(&total_content));
    println!(
        "{}  Total: {} batches ({} packets, {}){}{}",
        "│".bright_black(),
        format_number(total_batches).bright_white(),
        format_number(total_packets).bright_white(),
        format_bytes(total_bytes).bright_white(),
        " ".repeat(padding1),
        "│".bright_black()
    );

    // Line 2: Avg latency per packet/batch and total latency
    let avg_content = format!(
        "  Avg Latency: {} per packet ({} per batch) │ Total: {}",
        format_latency(avg_latency_per_packet),
        format_latency(avg_latency_per_batch),
        format_latency(counter.summary_latency)
    );
    let padding2 = TABLE_WIDTH.saturating_sub(display_width(&avg_content));
    println!(
        "{}  Avg Latency: {} per packet ({} per batch) {} Total: {}{}{}",
        "│".bright_black(),
        format_latency(avg_latency_per_packet).bright_cyan(),
        format_latency(avg_latency_per_batch).bright_cyan(),
        "│".bright_black(),
        format_latency(counter.summary_latency).bright_cyan(),
        " ".repeat(padding2),
        "│".bright_black()
    );

    if !counter.latencies.is_empty() {
        println!(
            "{}{}{}",
            "├".bright_black(),
            "─".repeat(TABLE_WIDTH).bright_black(),
            "┤".bright_black()
        );

        // Use global widths for consistent alignment across all histograms
        let max_left_val_width = widths.max_left_val_width;
        let max_right_val_width = widths.max_right_val_width;
        let max_count_width = widths.max_count_width;

        // Fixed bar width (reduced to accommodate larger count values)
        const BAR_WIDTH: usize = 32;

        // Display histogram with collapsing of consecutive zero-batch rows
        let mut i = 0;
        while i < counter.latencies.len() {
            let latency = &counter.latencies[i];

            // Check if this is the start of a sequence of zero-batch rows
            if latency.batches == 0 {
                // Find the end of consecutive zero-batch rows
                let mut j = i;
                while j < counter.latencies.len() && counter.latencies[j].batches == 0 {
                    j += 1;
                }

                // Determine the right boundary for the collapsed range
                let next_latency = if j < counter.latencies.len() {
                    Some(counter.latencies[j].min_latency)
                } else {
                    None
                };

                // Format left and right values of the collapsed range
                let left_val = format_latency(latency.min_latency as u64);
                let left_val_width = display_width(&left_val);
                let left_padding = max_left_val_width.saturating_sub(left_val_width);

                let range_str = if let Some(next) = next_latency {
                    let right_val = format_latency(next as u64);
                    let right_val_width = display_width(&right_val);
                    let right_padding = max_right_val_width.saturating_sub(right_val_width);
                    format!(
                        "{}{} - {}{}",
                        " ".repeat(left_padding),
                        left_val,
                        right_val,
                        " ".repeat(right_padding)
                    )
                } else {
                    // For the last row (e.g., "491.5µs+"), pad the right side to match width
                    format!(
                        "{}{}+{}",
                        " ".repeat(left_padding),
                        left_val,
                        " ".repeat(max_right_val_width + 2)
                    )
                };

                // Zero batches, so no bar
                let bar_padding = BAR_WIDTH;

                // Format count with right-alignment to max_count_width
                let count_str = format!("{:>width$}", format_number(0), width = max_count_width);

                // Format percentage
                let pct_str = format!("{:>4.1}%", 0.0);

                // Build the row content (without borders) to calculate display width
                let row_content = format!(
                    " {} │ {} │ {} ({})",
                    range_str,
                    " ".repeat(bar_padding),
                    count_str,
                    pct_str
                );
                let row_display_width = display_width(&row_content);
                let extra_padding = TABLE_WIDTH.saturating_sub(row_display_width);

                println!(
                    "{} {} {} {} {} {} ({}){}{}",
                    "│".bright_black(),
                    range_str.bright_white(),
                    "│".bright_black(),
                    " ".repeat(bar_padding),
                    "│".bright_black(),
                    count_str.bright_white(),
                    pct_str,
                    " ".repeat(extra_padding),
                    "│".bright_black()
                );

                i = j;
                continue;
            }

            // Display normal row (non-zero batches)
            let next_latency = counter.latencies.get(i + 1).map(|l| l.min_latency);
            let percentage = if total_batches > 0 {
                let calculated = (latency.batches as f64 / total_batches as f64) * 100.0;
                // Ensure non-zero batches show at least 0.01%
                if calculated > 0.0 && calculated < 0.01 {
                    0.01
                } else {
                    calculated
                }
            } else {
                0.0
            };

            // Format left and right values of the range
            let left_val = format_latency(latency.min_latency as u64);
            let left_val_width = display_width(&left_val);
            let left_padding = max_left_val_width.saturating_sub(left_val_width);

            let range_str = if let Some(next) = next_latency {
                let right_val = format_latency(next as u64);
                let right_val_width = display_width(&right_val);
                let right_padding = max_right_val_width.saturating_sub(right_val_width);
                format!(
                    "{}{} - {}{}",
                    " ".repeat(left_padding),
                    left_val,
                    right_val,
                    " ".repeat(right_padding)
                )
            } else {
                // For the last row (e.g., "491.5µs+"), pad the right side to match width
                // " - " is 3 chars, so we need "+  " (1 + 2 spaces to match " - ")
                format!(
                    "{}{}+{}",
                    " ".repeat(left_padding),
                    left_val,
                    " ".repeat(max_right_val_width + 2)
                )
            };

            // Calculate bar length relative to total batches (summary value)
            let bar_length = if total_batches > 0 {
                let calculated = ((latency.batches as f64 / total_batches as f64) * BAR_WIDTH as f64) as usize;
                // Ensure non-zero batches show at least 1 character bar
                if calculated == 0 && latency.batches > 0 {
                    1
                } else {
                    calculated
                }
            } else {
                0
            };
            let bar = "█".repeat(bar_length);
            let bar_padding = BAR_WIDTH.saturating_sub(bar_length);

            // Format count with right-alignment to max_count_width
            let count_str = format!("{:>width$}", format_number(latency.batches), width = max_count_width);

            // Format percentage
            let pct_str = format!("{:>4.1}%", percentage);

            // Build the row content (without borders) to calculate display width
            let row_content = format!(
                " {} │ {}{} │ {} ({})",
                range_str,
                bar,
                " ".repeat(bar_padding),
                count_str,
                pct_str
            );
            let row_display_width = display_width(&row_content);
            let extra_padding = TABLE_WIDTH.saturating_sub(row_display_width);

            println!(
                "{} {} {} {}{} {} {} ({}){}{}",
                "│".bright_black(),
                range_str.bright_white(),
                "│".bright_black(),
                bar.bright_green(),
                " ".repeat(bar_padding),
                "│".bright_black(),
                count_str.bright_white(),
                pct_str,
                " ".repeat(extra_padding),
                "│".bright_black()
            );

            i += 1;
        }

        // Calculate percentiles
        if total_batches > 0 {
            let (p50, p90, p99, max) = calculate_percentiles(&counter.latencies, total_batches);
            println!(
                "{}{}{}",
                "├".bright_black(),
                "─".repeat(TABLE_WIDTH).bright_black(),
                "┤".bright_black()
            );

            let inf = "inf";
            let p50_str = if p50 == 0 { inf.to_string() } else { format_latency(p50) };
            let p90_str = if p90 == 0 { inf.to_string() } else { format_latency(p90) };
            let p99_str = if p99 == 0 { inf.to_string() } else { format_latency(p99) };
            let max_str = if max == 0 { inf.to_string() } else { format_latency(max) };

            let percentile_content = format!(
                "  p50: {} │ p90: {} │ p99: {} │ max: {}",
                p50_str, p90_str, p99_str, max_str
            );
            let padding = TABLE_WIDTH.saturating_sub(display_width(&percentile_content));

            println!(
                "{}  p50: {} {} p90: {} {} p99: {} {} max: {}{}{}",
                "│".bright_black(),
                p50_str.bright_cyan(),
                "│".bright_black(),
                p90_str.bright_yellow(),
                "│".bright_black(),
                p99_str.bright_red(),
                "│".bright_black(),
                max_str.bright_magenta(),
                " ".repeat(padding),
                "│".bright_black()
            );
        }
    }

    println!(
        "{}{}{}",
        "└".bright_black(),
        "─".repeat(TABLE_WIDTH).bright_black(),
        "┘".bright_black()
    );
}

/// Calculate percentiles from histogram data
fn calculate_percentiles(latencies: &[LatencyRangeCounter], total_batches: u64) -> (u64, u64, u64, u64) {
    if latencies.is_empty() || total_batches == 0 {
        return (0, 0, 0, 0);
    }

    let p50_target = (total_batches as f64 * 0.50) as u64;
    let p90_target = (total_batches as f64 * 0.90) as u64;
    let p99_target = (total_batches as f64 * 0.99) as u64;
    let max_target = total_batches;

    let mut cumulative = 0u64;
    let mut p50 = 0u64;
    let mut p90 = 0u64;
    let mut p99 = 0u64;
    let mut max = 0u64;

    for idx in 1..latencies.len() {
        cumulative += latencies[idx - 1].batches;
        if p50 == 0 && cumulative >= p50_target {
            p50 = latencies[idx].min_latency as u64;
        }
        if p90 == 0 && cumulative >= p90_target {
            p90 = latencies[idx].min_latency as u64;
        }
        if p99 == 0 && cumulative >= p99_target {
            p99 = latencies[idx].min_latency as u64;
        }
        if max == 0 && cumulative >= max_target {
            max = latencies[idx].min_latency as u64;
        }
    }

    (p50, p90, p99, max)
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

/// Format large numbers with thousand separators or K/M/G/T suffixes for very
/// large numbers
fn format_number(n: u64) -> String {
    const THOUSAND: f64 = 1_000.0;
    const MILLION: f64 = 1_000_000.0;
    const BILLION: f64 = 1_000_000_000.0;
    const TRILLION: f64 = 1_000_000_000_000.0;

    let n_f = n as f64;

    // Use compact notation for numbers >= 100,000 to keep table width manageable
    if n_f >= TRILLION {
        format!("{:.2}T", n_f / TRILLION)
    } else if n_f >= BILLION {
        format!("{:.2}G", n_f / BILLION)
    } else if n_f >= MILLION {
        format!("{:.2}M", n_f / MILLION)
    } else if n_f >= 100_000.0 {
        format!("{:.2}K", n_f / THOUSAND)
    } else {
        // For smaller numbers, use thousand separators
        let s = n.to_string();
        let mut result = String::new();

        for (count, c) in s.chars().rev().enumerate() {
            if count > 0 && count % 3 == 0 {
                result.push(',');
            }
            result.push(c);
        }

        result.chars().rev().collect()
    }
}

/// Calculate display width of a string (accounting for multi-byte UTF-8
/// characters)
fn display_width(s: &str) -> usize {
    s.chars().count()
}

/// Format bytes in appropriate unit (B, KB, MB, GB, TB)
fn format_bytes(bytes: u64) -> String {
    const KB: f64 = 1024.0;
    const MB: f64 = KB * 1024.0;
    const GB: f64 = MB * 1024.0;
    const TB: f64 = GB * 1024.0;

    let bytes_f = bytes as f64;

    if bytes_f >= TB {
        format!("{:.2} TB", bytes_f / TB)
    } else if bytes_f >= GB {
        format!("{:.2} GB", bytes_f / GB)
    } else if bytes_f >= MB {
        format!("{:.2} MB", bytes_f / MB)
    } else if bytes_f >= KB {
        format!("{:.2} KB", bytes_f / KB)
    } else {
        format!("{} B", bytes)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_perf_counter_output() {
        // Create example performance counter data
        let response = PerfCountersResponse {
            tx: 1_234_567,
            rx: 1_234_567,
            tx_bytes: 1_288_490_188, // ~1.20 GB
            rx_bytes: 1_288_490_188,
            counters: vec![
                // Batch size 1
                PerfCounter {
                    min_batch_size: 1,
                    summary_latency: 401_500,
                    packets: 398,
                    bytes: 25_472,
                    latencies: vec![
                        LatencyRangeCounter { min_latency: 0, batches: 0 },
                        LatencyRangeCounter { min_latency: 10, batches: 0 },
                        LatencyRangeCounter { min_latency: 60, batches: 0 },
                        LatencyRangeCounter { min_latency: 110, batches: 0 },
                        LatencyRangeCounter { min_latency: 160, batches: 7 },
                        LatencyRangeCounter { min_latency: 210, batches: 7 },
                        LatencyRangeCounter { min_latency: 260, batches: 7 },
                        LatencyRangeCounter { min_latency: 310, batches: 9 },
                        LatencyRangeCounter { min_latency: 360, batches: 4 },
                        LatencyRangeCounter { min_latency: 410, batches: 4 },
                        LatencyRangeCounter { min_latency: 460, batches: 4 },
                        LatencyRangeCounter { min_latency: 510, batches: 4 },
                        LatencyRangeCounter { min_latency: 560, batches: 10 },
                        LatencyRangeCounter { min_latency: 610, batches: 11 },
                        LatencyRangeCounter { min_latency: 660, batches: 16 },
                        LatencyRangeCounter { min_latency: 710, batches: 15 },
                        LatencyRangeCounter { min_latency: 760, batches: 16 },
                        LatencyRangeCounter { min_latency: 810, batches: 18 },
                        LatencyRangeCounter { min_latency: 860, batches: 20 },
                        LatencyRangeCounter { min_latency: 910, batches: 15 },
                        LatencyRangeCounter { min_latency: 960, batches: 228 },
                        LatencyRangeCounter { min_latency: 1920, batches: 3 },
                        LatencyRangeCounter { min_latency: 3840, batches: 0 },
                        LatencyRangeCounter { min_latency: 7680, batches: 0 },
                        LatencyRangeCounter { min_latency: 15360, batches: 0 },
                        LatencyRangeCounter { min_latency: 30720, batches: 0 },
                        LatencyRangeCounter { min_latency: 61440, batches: 0 },
                        LatencyRangeCounter { min_latency: 122880, batches: 0 },
                        LatencyRangeCounter { min_latency: 245760, batches: 0 },
                        LatencyRangeCounter { min_latency: 491520, batches: 0 },
                    ],
                },
                // Batch size 2-31
                PerfCounter {
                    min_batch_size: 2,
                    summary_latency: 2_220_000,
                    packets: 45_678,
                    bytes: 45_678_000,
                    latencies: vec![
                        LatencyRangeCounter { min_latency: 200, batches: 12_025 },
                        LatencyRangeCounter { min_latency: 400, batches: 4_625 },
                        LatencyRangeCounter { min_latency: 800, batches: 1_295 },
                        LatencyRangeCounter { min_latency: 1600, batches: 370 },
                        LatencyRangeCounter { min_latency: 3200, batches: 185 },
                    ],
                },
                // Batch size 32+
                PerfCounter {
                    min_batch_size: 32,
                    summary_latency: 15_400_000,
                    packets: 1_024_000,
                    bytes: 1_024_000_000,
                    latencies: vec![
                        LatencyRangeCounter { min_latency: 200, batches: 20_800 },
                        LatencyRangeCounter { min_latency: 400, batches: 8_000 },
                        LatencyRangeCounter { min_latency: 800, batches: 2_240 },
                        LatencyRangeCounter { min_latency: 1600, batches: 640 },
                        LatencyRangeCounter { min_latency: 3200, batches: 320 },
                    ],
                },
            ],
        };

        // Display the formatted output
        println!("\n=== Example Performance Counter Output ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_uniform_distribution() {
        // Test with uniform distribution (all buckets have equal counts)
        let response = PerfCountersResponse {
            tx: 100_000,
            rx: 100_000,
            tx_bytes: 100_000_000,
            rx_bytes: 100_000_000,
            counters: vec![PerfCounter {
                min_batch_size: 1,
                summary_latency: 500_000,
                packets: 500,
                bytes: 1_024_000_000,
                latencies: vec![
                    LatencyRangeCounter { min_latency: 100, batches: 100 },
                    LatencyRangeCounter { min_latency: 200, batches: 100 },
                    LatencyRangeCounter { min_latency: 300, batches: 100 },
                    LatencyRangeCounter { min_latency: 400, batches: 100 },
                    LatencyRangeCounter { min_latency: 500, batches: 100 },
                ],
            }],
        };

        println!("\n=== Uniform Distribution Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_single_bucket() {
        // Test with all data in a single bucket
        let response = PerfCountersResponse {
            tx: 50_000,
            rx: 50_000,
            tx_bytes: 50_000_000,
            rx_bytes: 50_000_000,
            counters: vec![PerfCounter {
                min_batch_size: 1,
                summary_latency: 1_000_000,
                packets: 1000,
                bytes: 1_024_000_000,
                latencies: vec![
                    LatencyRangeCounter { min_latency: 100, batches: 0 },
                    LatencyRangeCounter { min_latency: 500, batches: 0 },
                    LatencyRangeCounter { min_latency: 1000, batches: 1000 },
                    LatencyRangeCounter { min_latency: 2000, batches: 0 },
                    LatencyRangeCounter { min_latency: 5000, batches: 0 },
                ],
            }],
        };

        println!("\n=== Single Bucket Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_large_numbers() {
        // Test with very large numbers
        let response = PerfCountersResponse {
            tx: 999_999_999_999,
            rx: 888_888_888_888,
            tx_bytes: 1_234_567_890_123_456,
            rx_bytes: 9_876_543_210_987_654,
            counters: vec![PerfCounter {
                min_batch_size: 64,
                summary_latency: 999_999_999_999,
                packets: 999_999_999,
                bytes: 63_999_999_936,
                latencies: vec![
                    LatencyRangeCounter {
                        min_latency: 1_000_000,
                        batches: 100_000_000,
                    },
                    LatencyRangeCounter {
                        min_latency: 10_000_000,
                        batches: 50_000_000,
                    },
                    LatencyRangeCounter {
                        min_latency: 100_000_000,
                        batches: 10_000_000,
                    },
                    LatencyRangeCounter {
                        min_latency: 1_000_000_000,
                        batches: 1_000_000,
                    },
                ],
            }],
        };

        println!("\n=== Large Numbers Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_small_numbers() {
        // Test with very small numbers
        let response = PerfCountersResponse {
            tx: 10,
            rx: 5,
            tx_bytes: 1000,
            rx_bytes: 500,
            counters: vec![PerfCounter {
                min_batch_size: 1,
                summary_latency: 100,
                packets: 10,
                bytes: 640,
                latencies: vec![
                    LatencyRangeCounter { min_latency: 1, batches: 3 },
                    LatencyRangeCounter { min_latency: 5, batches: 4 },
                    LatencyRangeCounter { min_latency: 10, batches: 2 },
                    LatencyRangeCounter { min_latency: 20, batches: 1 },
                ],
            }],
        };

        println!("\n=== Small Numbers Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_empty_histogram() {
        // Test with empty histogram (all zeros)
        let response = PerfCountersResponse {
            tx: 0,
            rx: 0,
            tx_bytes: 0,
            rx_bytes: 0,
            counters: vec![PerfCounter {
                min_batch_size: 1,
                summary_latency: 0,
                packets: 0,
                bytes: 0,
                latencies: vec![
                    LatencyRangeCounter { min_latency: 100, batches: 0 },
                    LatencyRangeCounter { min_latency: 200, batches: 0 },
                    LatencyRangeCounter { min_latency: 300, batches: 0 },
                ],
            }],
        };

        println!("\n=== Empty Histogram Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_bimodal_distribution() {
        // Test with bimodal distribution (two peaks)
        let response = PerfCountersResponse {
            tx: 200_000,
            rx: 200_000,
            tx_bytes: 200_000_000,
            rx_bytes: 200_000_000,
            counters: vec![PerfCounter {
                min_batch_size: 8,
                summary_latency: 5_000_000,
                packets: 2000,
                bytes: 128_000,
                latencies: vec![
                    LatencyRangeCounter { min_latency: 100, batches: 500 },
                    LatencyRangeCounter { min_latency: 200, batches: 50 },
                    LatencyRangeCounter { min_latency: 300, batches: 10 },
                    LatencyRangeCounter { min_latency: 400, batches: 50 },
                    LatencyRangeCounter { min_latency: 500, batches: 500 },
                    LatencyRangeCounter { min_latency: 1000, batches: 100 },
                ],
            }],
        };

        println!("\n=== Bimodal Distribution Test ===\n");
        format_perf_counters(&response);
    }

    #[test]
    fn test_perf_counter_asymmetric_rx_tx() {
        // Test with very different RX and TX values
        let response = PerfCountersResponse {
            tx: 1,
            rx: 999_999_999,
            tx_bytes: 64,
            rx_bytes: 999_999_999_999,
            counters: vec![PerfCounter {
                min_batch_size: 1,
                summary_latency: 1000,
                packets: 100,
                bytes: 6_400,
                latencies: vec![LatencyRangeCounter { min_latency: 10, batches: 100 }],
            }],
        };

        println!("\n=== Asymmetric RX/TX Test ===\n");
        format_perf_counters(&response);
    }
}
