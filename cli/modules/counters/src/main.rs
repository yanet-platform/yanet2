//! CLI for YANET "counters" module.

use bytesize::ByteSize;
use clap::{ArgAction, Parser};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    counters_service_client::CountersServiceClient, CounterTag, CountersByTagsRequest, CountersByTagsResponse,
    PortCountersRequest, PortCountersResponse, WorkerCounter, WorkerCountersRequest, WorkerCountersResponse,
};

const COUNTERS_SERVICE: &str = "controlplane.ynpb.v1.CountersService";

/// Counters module - displays counters information.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: Option<ModeCmd>,
    #[command(flatten)]
    pub by_tags: ByTagsCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, clap::Args, Default)]
pub struct ByTagsCmd {
    /// Counter names to show, each an exact name or a Rust regex pattern.
    #[arg(value_name = "PATTERN")]
    pub names: Vec<String>,
    /// Device name to filter by.
    #[arg(long, short = 'd')]
    pub device: Option<String>,
    /// Pipeline name to filter by.
    #[arg(long, short = 'p')]
    pub pipeline: Option<String>,
    /// Function name to filter by.
    #[arg(long, short = 'f')]
    pub function: Option<String>,
    /// Chain name to filter by.
    #[arg(long, short = 'c')]
    pub chain: Option<String>,
    /// Module type to filter by.
    #[arg(long, short_alias = 't')]
    pub module_type: Option<String>,
    /// Module name to filter by.
    #[arg(long, short = 'm')]
    pub module_name: Option<String>,
    /// Owner level to filter by (device, pipeline, function, chain, module,
    /// object).
    #[arg(long)]
    pub kind: Option<String>,
}

impl From<ByTagsCmd> for CountersByTagsRequest {
    fn from(cmd: ByTagsCmd) -> Self {
        let mut tags = Vec::new();

        if let Some(value) = cmd.device {
            tags.push(CounterTag { key: "device".to_string(), value });
        }
        if let Some(value) = cmd.pipeline {
            tags.push(CounterTag { key: "pipeline".to_string(), value });
        }
        if let Some(value) = cmd.function {
            tags.push(CounterTag { key: "function".to_string(), value });
        }
        if let Some(value) = cmd.chain {
            tags.push(CounterTag { key: "chain".to_string(), value });
        }
        if let Some(value) = cmd.module_type {
            tags.push(CounterTag {
                key: "module_type".to_string(),
                value,
            });
        }
        if let Some(value) = cmd.module_name {
            tags.push(CounterTag {
                key: "module_name".to_string(),
                value,
            });
        }
        if let Some(value) = cmd.kind {
            tags.push(CounterTag { key: "kind".to_string(), value });
        }

        Self { tags, query: cmd.names }
    }
}

#[derive(Debug, Clone, clap::Subcommand)]
pub enum ModeCmd {
    /// Show worker counters.
    Workers,
    /// Show port counters.
    Ports,
}

impl ModeCmd {
    pub fn action(&self) -> &'static str {
        match self {
            ModeCmd::Workers => "show worker counters",
            ModeCmd::Ports => "show port counters",
        }
    }
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.as_ref().map_or("show counters", ModeCmd::action);
    let mut service = CountersService::new(&cmd.connection, action).await?;

    match cmd.mode {
        Some(ModeCmd::Workers) => {
            let response = service.workers().await?;
            output::data(
                || &response,
                || {
                    format_worker_counters(&response);
                },
            );
        }
        Some(ModeCmd::Ports) => {
            let response = service.ports().await?;
            output::data(
                || &response,
                || {
                    print!(
                        "{}",
                        serde_yaml::to_string(&response).expect("counters YAML serialization must not fail")
                    );

                    if response.ports.is_empty() {
                        output::empty(format_args!("No port counters found."));
                    }
                },
            );
        }
        None => {
            let response = service.by_tags(cmd.by_tags.into()).await?;
            output::data(
                || &response,
                || {
                    print!(
                        "{}",
                        serde_yaml::to_string(&response).expect("counters YAML serialization must not fail")
                    );

                    if response.groups.is_empty() {
                        output::empty(format_args!("No counters found."));
                    }
                },
            );
        }
    }

    Ok(())
}

pub struct CountersService {
    service: Service<CountersServiceClient<LayeredChannel>>,
    action: &'static str,
}

impl CountersService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, COUNTERS_SERVICE, |channel| {
            CountersServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service, action })
    }

    pub async fn by_tags(&mut self, request: CountersByTagsRequest) -> Result<CountersByTagsResponse, Error> {
        Ok(self
            .service
            .client()
            .by_tags(request)
            .await
            .map_err(self.service.status(self.action))?
            .into_inner())
    }

    pub async fn workers(&mut self) -> Result<WorkerCountersResponse, Error> {
        Ok(self
            .service
            .client()
            .workers(WorkerCountersRequest {})
            .await
            .map_err(self.service.status(self.action))?
            .into_inner())
    }

    pub async fn ports(&mut self) -> Result<PortCountersResponse, Error> {
        Ok(self
            .service
            .client()
            .ports(PortCountersRequest {})
            .await
            .map_err(self.service.status(self.action))?
            .into_inner())
    }
}

/// A displayable summary row for one worker in the workers table.
#[derive(Debug, Tabled)]
struct WorkerRow {
    #[tabled(rename = "Worker")]
    worker: u32,
    #[tabled(rename = "Core")]
    core: u32,
    #[tabled(rename = "Device")]
    device: u32,
    #[tabled(rename = "Queue")]
    queue: u32,
    #[tabled(rename = "Iterations")]
    iterations: String,
    #[tabled(rename = "RX")]
    rx: String,
    #[tabled(rename = "TX")]
    tx: String,
    #[tabled(rename = "Empty %")]
    empty_pct: String,
    #[tabled(rename = "Avg burst")]
    avg_burst: String,
    #[tabled(rename = "Remote RX")]
    remote_rx: String,
    #[tabled(rename = "Remote TX")]
    remote_tx: String,
    #[tabled(rename = "LclTX Drp")]
    local_tx_drops: String,
    #[tabled(rename = "RemTX Drp")]
    remote_tx_drops: String,
    #[tabled(rename = "Disposed")]
    disposed: String,
}

impl From<&WorkerCounter> for WorkerRow {
    fn from(w: &WorkerCounter) -> Self {
        let total_polls: u64 = w.rx_bursts.iter().sum();
        let empty_polls = w.rx_bursts.first().copied().unwrap_or(0);
        let non_empty_polls = total_polls - empty_polls;

        let empty_pct = if total_polls > 0 {
            format!("{:.1}%", empty_polls as f64 / total_polls as f64 * 100.0)
        } else {
            "0.0%".to_string()
        };

        let avg_burst = if non_empty_polls > 0 {
            format!("{:.1}", w.rx_packets as f64 / non_empty_polls as f64)
        } else {
            "n/a".to_string()
        };

        Self {
            worker: w.worker_idx,
            core: w.core_id,
            device: w.device_id,
            queue: w.queue_id,
            iterations: format_number(w.iterations),
            rx: if w.rx_bytes > 0 {
                format!("{} ({})", format_number(w.rx_packets), ByteSize::b(w.rx_bytes))
            } else {
                format_number(w.rx_packets)
            },
            tx: if w.tx_bytes > 0 {
                format!("{} ({})", format_number(w.tx_packets), ByteSize::b(w.tx_bytes))
            } else {
                format_number(w.tx_packets)
            },
            empty_pct,
            avg_burst,
            remote_rx: format_number(w.remote_rx_packets),
            remote_tx: format_number(w.remote_tx_packets),
            local_tx_drops: format_number(w.local_tx_drops),
            remote_tx_drops: format_number(w.remote_tx_drops),
            disposed: format_number(w.disposed),
        }
    }
}

/// Renders the worker counters summary table and per-worker rx-burst
/// histograms.
fn format_worker_counters(response: &WorkerCountersResponse) {
    if response.workers.is_empty() {
        output::empty(format_args!("No worker counters found."));
        return;
    }

    let rows: Vec<WorkerRow> = response.workers.iter().map(WorkerRow::from).collect();
    ync::display::print_table_from_entries(rows);

    for worker in &response.workers {
        print_worker_histogram(worker);
    }
}

/// Prints the rx-burst histogram for one worker in the metrics style.
fn print_worker_histogram(worker: &WorkerCounter) {
    let bursts = &worker.rx_bursts;
    if bursts.is_empty() {
        return;
    }

    println!("worker {} rx bursts", worker.worker_idx);

    let max_count = bursts.iter().copied().max().unwrap_or(0);
    let wl = bursts.len().saturating_sub(1).to_string().len().max("0".len());
    let wc = bursts.iter().map(|c| c.to_string().len()).max().unwrap_or(0);

    for (idx, &count) in bursts.iter().enumerate() {
        let bars = "∎".repeat(ync::display::bar_len(count, max_count));
        println!("  {idx:>wl$} [ {count:>wc$} ] {bars}");
    }

    println!();
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
