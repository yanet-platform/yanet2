//! CLI for YANET "counters" module.

use bytesize::ByteSize;
use clap::{ArgAction, Parser};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    metrics,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    CounterTag, CountersByTagsRequest, CountersByTagsResponse, PortCountersRequest, PortCountersResponse,
    WorkerCounter, WorkerCountersRequest, WorkerCountersResponse, WorkerRxMempool,
    counters_service_client::CountersServiceClient,
};

const COUNTERS_SERVICE: &str = "controlplane.ynpb.v1.CountersService";

/// Displays dataplane counters.
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
    /// Be verbose: shows debug log lines and raw gRPC error details.
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

fn main() -> std::process::ExitCode {
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
    #[tabled(rename = "RX pool free")]
    rx_pool_free: String,
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
            iterations: format_compact(w.iterations),
            rx: if w.rx_bytes > 0 {
                format!("{} ({})", format_compact(w.rx_packets), ByteSize::b(w.rx_bytes))
            } else {
                format_compact(w.rx_packets)
            },
            tx: if w.tx_bytes > 0 {
                format!("{} ({})", format_compact(w.tx_packets), ByteSize::b(w.tx_bytes))
            } else {
                format_compact(w.tx_packets)
            },
            empty_pct,
            avg_burst,
            remote_rx: format_compact(w.remote_rx_packets),
            remote_tx: format_compact(w.remote_tx_packets),
            local_tx_drops: format_compact(w.local_tx_drops),
            remote_tx_drops: format_compact(w.remote_tx_drops),
            disposed: format_compact(w.disposed),
            rx_pool_free: format_rx_pool_free(w.rx_mempool.as_ref()),
        }
    }
}

/// Renders one worker's RX pool free objects as `available/capacity`.
///
/// An absent pool stays `n/a`: the serving side predates pool gauges,
/// which presence on the wire distinguishes from any real occupancy.
fn format_rx_pool_free(pool: Option<&WorkerRxMempool>) -> String {
    match pool {
        None => "n/a".to_string(),
        Some(pool) => format!("{}/{}", pool.available, pool.capacity),
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
fn format_compact(n: u64) -> String {
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
        metrics::format_number(n)
    }
}

#[cfg(test)]
mod test {
    use ynpb::pb::WorkerRxMempool;

    use super::format_rx_pool_free;

    fn pool(available: u32, capacity: u32) -> Option<WorkerRxMempool> {
        Some(WorkerRxMempool { capacity, available })
    }

    #[test]
    fn test_format_rx_pool_free_absent_pool_is_na() {
        assert_eq!(format_rx_pool_free(None), "n/a");
    }

    #[test]
    fn test_format_rx_pool_free_full_pool() {
        assert_eq!(format_rx_pool_free(pool(16384, 16384).as_ref()), "16384/16384");
    }

    #[test]
    fn test_format_rx_pool_free_partially_used_pool() {
        assert_eq!(format_rx_pool_free(pool(9728, 16384).as_ref()), "9728/16384");
    }

    #[test]
    fn test_format_rx_pool_free_exhausted_pool() {
        assert_eq!(format_rx_pool_free(pool(0, 16384).as_ref()), "0/16384");
    }
}
