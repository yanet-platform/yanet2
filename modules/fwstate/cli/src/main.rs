use core::{
    fmt,
    net::{IpAddr, Ipv6Addr},
};
use std::collections::HashMap;

use args::{
    DeleteCmd, DirectionArg, EntriesCmd, Family, LinkCmd, MapCmd, MetricsCmd, ModeCmd, ShowCmd, StatsCmd, UpdateCmd,
};
use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::{CompleteEnv, engine::CompletionCandidate};
use commonpb::pb::{GetMetricsRequest, IpAddress, Metric as ProtoMetric};
use fwstatemappb::{
    CreateMapRequest, DeleteMapRequest, ListMapsRequest, fw_state_map_service_client::FwStateMapServiceClient,
};
use fwstatepb::{
    DeleteConfigRequest, Direction, GetStatsRequest, LinkFwStateRequest, ListConfigsRequest, ListEntriesRequest,
    ShowConfigRequest, UpdateConfigRequest, fw_state_service_client::FwStateServiceClient,
    metrics_service_client::MetricsServiceClient,
};
use tabled::Tabled;
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Status, codec::CompressionEncoding};
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    metrics::{self, GaugeRow, Kind, Metric},
    output::{self, CommonFormat},
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("modules.fwstate.controlplane.fwstatepb.v1");
}

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatemappb {
    use serde::Serialize;

    tonic::include_proto!("objects.fwstate.controlplane.fwstatemappb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.FWStateService";

/// The fully-qualified gRPC service name for the metrics service.
const METRICS_SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.MetricsService";

/// The fully-qualified gRPC service name for the fwstate-map service.
const MAP_SERVICE_NAME: &str = "objects.fwstate.controlplane.fwstatemappb.v1.FWStateMapService";

/// FWState module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Parse IPv6 address string into an `IpAddress` proto message.
fn parse_ipv6(s: &str) -> Result<IpAddress, String> {
    let addr = s.parse::<Ipv6Addr>().map_err(|err| err.to_string())?;
    Ok(IpAddress { addr: addr.octets().to_vec() })
}

pub struct FWStateService {
    service: Service<FwStateServiceClient<LayeredChannel>>,
    metrics: Service<MetricsServiceClient<LayeredChannel>>,
    maps: Service<FwStateMapServiceClient<LayeredChannel>>,
}

/// State an `entries` dump carries across its batches and both maps.
struct DumpState {
    /// Entries printed so far, which `--count` limits.
    printed: u32,
    /// Whether the human-readable header row is already out. Deferred until
    /// the first entry arrives, so a zero-entry result prints no header.
    header_printed: bool,
    /// Config generation the last response reported.
    generation: Option<u64>,
}

impl DumpState {
    fn new() -> Self {
        Self {
            printed: 0,
            header_printed: false,
            generation: None,
        }
    }

    /// Warns when a response reports a different generation than the one
    /// before it.
    ///
    /// A bump means layers were relinked, so the cursor and `--layer` stop
    /// denoting what they did when the dump began, and the remaining
    /// entries can repeat or be missed. Rows already printed were accurate
    /// when read, so the dump goes on and only warns.
    fn note_generation(&mut self, generation: u64) {
        match self.generation.replace(generation) {
            Some(previous) if previous != generation => log::warn!(
                "fwstate config changed mid-dump (generation {previous} -> {generation}): \
                 entries may repeat or be missed"
            ),
            _ => {}
        }
    }
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let metrics = Service::new(&conn, METRICS_SERVICE_NAME, |channel| {
            MetricsServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let maps = Service::new(&conn, MAP_SERVICE_NAME, |channel| {
            FwStateMapServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });

        Ok(Self { service, metrics, maps })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No FWState configurations found."),
                        format_args!(
                            "create one with 'yanet-cli-fwstate update --name <name> --src-addr <addr> --dst-addr-multicast <addr> --port-multicast <port>'"
                        ),
                    );
                    return;
                }

                println!(
                    "{}",
                    serde_json::to_string_pretty(&response.configs)
                        .expect("fwstate config list JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: false,
        };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(
            || &response,
            || {
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response).expect("fwstate config JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted fwstate config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        // First, fetch the current config to merge with new values
        let current_request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: true,
        };
        let current_response = self.service.client().show_config(current_request).await;
        let (mut map_config, mut sync_config) = match current_response {
            Ok(resp) => {
                let msg = resp.into_inner();
                (msg.map_config.unwrap_or_default(), msg.sync_config.unwrap_or_default())
            }
            _ => (Default::default(), Default::default()),
        };

        // Update map config fields if provided
        if let Some(index_size) = cmd.index_size {
            map_config.index_size = index_size;
        }

        if let Some(extra_bucket_count) = cmd.extra_bucket_count {
            map_config.extra_bucket_count = extra_bucket_count;
        }

        // Update only the fields that were provided
        if let Some(ref src_addr) = cmd.src_addr {
            sync_config.src_addr = Some(parse_ipv6(src_addr).map_err(|err| self.service.invalid("update", err))?);
        }

        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast =
                Some(parse_ipv6(dst_addr_multicast).map_err(|err| self.service.invalid("update", err))?);
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = port_multicast;
        }

        // Convert timeouts from Duration to nanoseconds if provided
        if let Some(tcp_syn_ack) = cmd.tcp_syn_ack {
            sync_config.tcp_syn_ack = tcp_syn_ack.as_nanos() as u64;
        }

        if let Some(tcp_syn) = cmd.tcp_syn {
            sync_config.tcp_syn = tcp_syn.as_nanos() as u64;
        }

        if let Some(tcp_fin) = cmd.tcp_fin {
            sync_config.tcp_fin = tcp_fin.as_nanos() as u64;
        }

        if let Some(tcp) = cmd.tcp {
            sync_config.tcp = tcp.as_nanos() as u64;
        }

        if let Some(udp) = cmd.udp {
            sync_config.udp = udp.as_nanos() as u64;
        }

        if let Some(default) = cmd.default {
            sync_config.default = default.as_nanos() as u64;
        }

        if let Some(suppress) = cmd.sync_suppress_timeout {
            sync_config.sync_suppress_timeout = Some(suppress.as_nanos() as u64);
        }

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            map_config: Some(map_config),
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated fwstate config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn link_fwstate(&mut self, cmd: LinkCmd) -> Result<(), Error> {
        let request = LinkFwStateRequest {
            fwstate_name: cmd.config_name.clone(),
            acl_config_names: cmd.acl_configs.clone(),
        };
        log::trace!("LinkFwStateRequest: {request:?}");
        self.service
            .client()
            .link_fw_state(request)
            .await
            .map_err(self.service.status("link"))?;

        output::success(
            "link",
            format_args!(
                "Linked fwstate {} to ACL config(s) {}.",
                cmd.config_name,
                cmd.acl_configs.join(", ")
            ),
        );

        Ok(())
    }

    pub async fn get_stats(&mut self, cmd: StatsCmd) -> Result<(), Error> {
        let request = GetStatsRequest { name: cmd.config_name.clone() };
        log::trace!("GetStatsRequest: {request:?}");
        let response = self
            .service
            .client()
            .get_stats(request)
            .await
            .map_err(self.service.status("stats"))?
            .into_inner();

        output::data(
            || &response,
            || {
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response).expect("fwstate stats JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn list_entries(&mut self, cmd: EntriesCmd, format: CommonFormat) -> Result<(), Error> {
        let direction = match cmd.direction {
            DirectionArg::Forward => Direction::Forward,
            DirectionArg::Backward => Direction::Backward,
        };

        let limit = cmd.count;
        let mut state = DumpState::new();

        for family in cmd.families() {
            if limit > 0 && state.printed >= limit {
                break;
            }
            self.list_entries_map(&cmd, family, direction, format, &mut state)
                .await?;
        }

        if state.printed == 0 {
            output::empty(format_args!(
                "No firewall state entries found for '{}'.",
                cmd.config_name
            ));
        }

        Ok(())
    }

    async fn list_entries_map(
        &mut self,
        cmd: &EntriesCmd,
        family: Family,
        direction: Direction,
        format: CommonFormat,
        state: &mut DumpState,
    ) -> Result<(), Error> {
        let limit = cmd.count;
        let (tx, rx) = mpsc::channel(1);
        let stream = ReceiverStream::new(rx);

        let initial_req = ListEntriesRequest {
            config_name: cmd.config_name.clone(),
            is_ipv6: family.is_ipv6(),
            layer_index: cmd.layer,
            include_expired: cmd.include_expired,
            direction: direction as i32,
            batch_size: cmd.batch,
            index: cmd.index as i64,
        };
        tx.send(initial_req)
            .await
            .map_err(|err| self.service.status("list entries")(Status::internal(format!("send error: {err}"))))?;

        let mut response_stream = self
            .service
            .client()
            .list_entries(stream)
            .await
            .map_err(self.service.status("list entries"))?
            .into_inner();

        while let Some(resp) = response_stream
            .message()
            .await
            .map_err(self.service.status("list entries"))?
        {
            state.note_generation(resp.generation);

            for entry in &resp.entries {
                if limit > 0 && state.printed >= limit {
                    break;
                }

                match format {
                    CommonFormat::Human => {
                        if !state.header_printed {
                            println!(
                                "{:<6} {:<48} {:<48} {:<8} {:<9} {:<7}",
                                "IDX", "SRC", "DST", "PROTO", "FLAGS S|D", "EXPRD"
                            );
                            state.header_printed = true;
                        }

                        print_entry(entry);
                    }
                    CommonFormat::Json => {
                        println!(
                            "{}",
                            serde_json::to_string(entry).expect("fwstate entry JSON serialization must not fail")
                        );
                    }
                }

                state.printed += 1;
            }

            if (limit > 0 && state.printed >= limit) || !resp.has_more {
                break;
            }

            let next_req = ListEntriesRequest {
                config_name: cmd.config_name.clone(),
                is_ipv6: family.is_ipv6(),
                layer_index: cmd.layer,
                include_expired: cmd.include_expired,
                direction: direction as i32,
                batch_size: cmd.batch,
                index: resp.index,
            };
            tx.send(next_req)
                .await
                .map_err(|err| self.service.status("list entries")(Status::internal(format!("send error: {err}"))))?;
        }

        Ok(())
    }

    pub async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Error> {
        let response = self
            .metrics
            .client()
            .get_metrics(GetMetricsRequest::default())
            .await
            .map_err(self.metrics.status("metrics"))?
            .into_inner();
        let mut label_filters: Vec<(&str, &str)> = Vec::with_capacity(cmd.labels.len());
        for s in &cmd.labels {
            match s.split_once('=') {
                Some(kv) => label_filters.push(kv),
                None => {
                    return Err(self
                        .metrics
                        .invalid("metrics", format!("invalid label filter {s:?}, expected KEY=VALUE")));
                }
            }
        }

        let metrics: Vec<ProtoMetric> = response
            .metrics
            .into_iter()
            .filter(|m| {
                if let Some(ref f) = cmd.name
                    && !f.matches(m)
                {
                    return false;
                }
                label_filters
                    .iter()
                    .all(|(k, v)| metrics::proto_label_value(m, k) == Some(v))
            })
            .collect();

        output::data(
            || &metrics,
            || {
                if metrics.is_empty() {
                    match cmd.name.as_ref().and_then(ValueEnum::to_possible_value) {
                        Some(name) => {
                            output::empty(format_args!("No FWState metrics found for '{}'.", name.get_name()))
                        }
                        None => output::empty(format_args!("No FWState metrics found.")),
                    }
                    return;
                }

                let metrics: Vec<Metric> = metrics.iter().cloned().map(Metric::from_proto).collect();
                print_metrics_table(&metrics)
            },
        );

        Ok(())
    }

    pub async fn manage_maps(&mut self, cmd: MapCmd) -> Result<(), Error> {
        match cmd {
            MapCmd::Create(cmd) => self.map_create(cmd).await,
            MapCmd::Delete(cmd) => self.map_delete(cmd).await,
            MapCmd::List(cmd) => self.map_list(cmd).await,
        }
    }

    pub async fn map_create(&mut self, cmd: args::MapCreateCmd) -> Result<(), Error> {
        let request = CreateMapRequest {
            name: cmd.map_name.clone(),
            kind: match cmd.kind {
                args::MapKind::V4 => fwstatemappb::Kind::V4.into(),
                args::MapKind::V6 => fwstatemappb::Kind::V6.into(),
            },
            index_size: cmd.index_size.unwrap_or(0),
            extra_bucket_count: cmd.extra_bucket_count.unwrap_or(0),
            worker_count: cmd.worker_count.unwrap_or(0),
        };
        log::trace!("CreateMapRequest: {request:?}");
        self.maps
            .client()
            .create_map(request)
            .await
            .map_err(self.maps.status("map create"))?;

        output::success("map create", format_args!("Created fwstate-map {}.", cmd.map_name));

        Ok(())
    }

    pub async fn map_delete(&mut self, cmd: args::MapDeleteCmd) -> Result<(), Error> {
        let request = DeleteMapRequest { name: cmd.map_name.clone() };
        log::trace!("DeleteMapRequest: {request:?}");
        self.maps
            .client()
            .delete_map(request)
            .await
            .map_err(self.maps.status("map delete"))?;

        output::success("map delete", format_args!("Deleted fwstate-map {}.", cmd.map_name));

        Ok(())
    }

    pub async fn map_list(&mut self, _cmd: args::MapListCmd) -> Result<(), Error> {
        let request = ListMapsRequest {};
        let response = self
            .maps
            .client()
            .list_maps(request)
            .await
            .map_err(self.maps.status("map list"))?
            .into_inner();

        output::data(
            || &response.maps,
            || {
                if response.maps.is_empty() {
                    output::empty_with_hint(
                        format_args!("No fwstate-map objects found."),
                        format_args!("create one with 'yanet-cli-fwstate map create --name <name> --kind <v4|v6>'"),
                    );
                    return;
                }

                println!(
                    "{}",
                    serde_json::to_string_pretty(&response.maps)
                        .expect("fwstate-map list JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }
}

/// Formats an address and port as an endpoint, bracketing IPv6.
///
/// Without brackets the port merges into the trailing hextet: `2001:db8::2`
/// on port 80 would read as `2001:db8::2:80`. An IPv4-mapped IPv6 address is
/// unmapped first, and a malformed one reads as `invalid`, both as
/// `IpAddress` renders them on its own. An absent address reads as `?`.
fn format_endpoint(addr: Option<&IpAddress>, port: u32) -> String {
    let Some(addr) = addr else {
        return format!("?:{port}");
    };

    match IpAddr::try_from(addr) {
        Ok(IpAddr::V4(v4)) => format!("{v4}:{port}"),
        Ok(IpAddr::V6(v6)) => match v6.to_ipv4_mapped() {
            Some(v4) => format!("{v4}:{port}"),
            None => format!("[{v6}]:{port}"),
        },
        Err(..) => format!("invalid:{port}"),
    }
}

/// Format IANA protocol number as a human-readable name.
/// See: https://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml
fn format_proto(proto: u32) -> String {
    match proto {
        1 => "ICMP".into(),
        4 => "IPv4".into(),
        6 => "TCP".into(),
        17 => "UDP".into(),
        41 => "IPv6".into(),
        47 => "GRE".into(),
        58 => "ICMPv6".into(),
        132 => "SCTP".into(),
        _ => proto.to_string(),
    }
}

/// Decoded TCP flags for a single direction (4-bit nibble).
///
/// Bit layout (from [`lib/fwstate/types.h`]):
///   - 0x01 = FIN
///   - 0x02 = SYN
///   - 0x04 = RST
///   - 0x08 = ACK
struct TcpNibble(u8);

const TCP_FLAG_TABLE: [(u8, char); 4] = [(0x08, 'A'), (0x02, 'S'), (0x04, 'R'), (0x01, 'F')];

impl fmt::Display for TcpNibble {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (mask, ch) in TCP_FLAG_TABLE {
            if self.0 & mask != 0 {
                write!(f, "{ch}")?;
            } else {
                f.write_str("-")?;
            }
        }
        Ok(())
    }
}

/// Firewall state flags byte containing src (lower nibble) and dst
/// (upper nibble) TCP flag sets.
///
/// The raw byte is stored in `fw_state_value.flags` and transmitted
/// via protobuf as `FwStateValue.flags`.
/// See `struct fw_state_flags` (from `lib/fwstate/types.h`)
struct FwStateFlags(u32);

impl FwStateFlags {
    fn src(&self) -> TcpNibble {
        TcpNibble((self.0 & 0x0f) as u8)
    }

    fn dst(&self) -> TcpNibble {
        TcpNibble(((self.0 >> 4) & 0x0f) as u8)
    }
}

impl fmt::Display for FwStateFlags {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}|{}", self.src(), self.dst())
    }
}

fn print_entry(entry: &fwstatepb::FwStateEntry) {
    let (src, dst, proto) = match &entry.key {
        Some(key) => (
            format_endpoint(key.src_addr.as_ref(), key.src_port),
            format_endpoint(key.dst_addr.as_ref(), key.dst_port),
            key.proto,
        ),
        None => (format_endpoint(None, 0), format_endpoint(None, 0), 0),
    };

    let flags = entry.value.as_ref().map(|v| v.flags).unwrap_or(0);

    println!(
        "{:<6} {:<48} {:<48} {:<8} {:<9} {:<7}",
        entry.idx,
        src,
        dst,
        format_proto(proto),
        FwStateFlags(flags),
        if entry.expired { "yes" } else { "no" },
    );
}

#[derive(Tabled)]
struct CounterRow {
    #[tabled(rename = "Counter")]
    counter: String,
    #[tabled(rename = "Packets")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
    #[tabled(rename = "Entries")]
    entries: String,
}

fn print_metrics_table(metrics: &[Metric]) {
    struct CounterPair {
        display: String,
        packets: Option<u64>,
        bytes: Option<u64>,
        entries: Option<u64>,
    }

    let mut counter_keys: Vec<String> = Vec::new();
    let mut counter_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut gauge_keys: Vec<String> = Vec::new();
    let mut gauge_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut grpc_counters: Vec<&Metric> = Vec::new();
    let mut grpc_histograms: Vec<&Metric> = Vec::new();

    for m in metrics {
        if m.name.starts_with("grpc_") {
            match m.kind {
                Kind::Counter => grpc_counters.push(m),
                Kind::Histogram => grpc_histograms.push(m),
                _ => {}
            }
            continue;
        }

        match m.kind {
            Kind::Gauge => {
                let cfg = format!(
                    "{}\0{}",
                    m.label_value("config").unwrap_or("global"),
                    m.label_value("af").unwrap_or(""),
                );
                if !gauge_map.contains_key(&cfg) {
                    gauge_keys.push(cfg.clone());
                }
                gauge_map.entry(cfg).or_default().push(m);
            }
            Kind::Counter => {
                let key = format!(
                    "{}\0{}\0{}\0{}\0{}",
                    m.label_value("config").unwrap_or(""),
                    m.label_value("device").unwrap_or(""),
                    m.label_value("pipeline").unwrap_or(""),
                    m.label_value("function").unwrap_or(""),
                    m.label_value("chain").unwrap_or(""),
                );
                if !counter_map.contains_key(&key) {
                    counter_keys.push(key.clone());
                }
                counter_map.entry(key).or_default().push(m);
            }
            _ => {}
        }
    }

    for key in &counter_keys {
        let counters = &counter_map[key];
        let parts: Vec<&str> = key.split('\0').collect();
        let (config, device, pipeline, function, chain) = (parts[0], parts[1], parts[2], parts[3], parts[4]);
        println!(
            "FWSTATE COUNTERS  config={config} device={device} pipeline={pipeline} function={function} chain={chain}"
        );
        println!();

        let mut pair_order: Vec<String> = Vec::new();
        let mut pair_map: HashMap<String, CounterPair> = HashMap::new();

        // Generic counters (the default arm of emitCounterMetrics) are all
        // exported under the shared names fwstate_counter_packets /
        // fwstate_counter_bytes and distinguished only by the "counter" label.
        // After stripping the fwstate_ prefix and the _packets/_bytes suffix
        // they all collapse to the same base "counter", so the pair key must
        // include the "counter" label value to keep them as separate rows
        // instead of overwriting each other (last write wins). Dedicated
        // counters carry no "counter" label, so the suffix is empty and their
        // base is unaffected.
        for m in counters {
            let val = m.value.unwrap_or(0.0) as u64;
            let stripped = m.name.strip_prefix("fwstate_").unwrap_or(&m.name);
            let counter_label = m.label_value("counter").unwrap_or("");

            // Determine the semantic suffix and the base name shared by the
            // packets/bytes series of the same counter. The pair key and the
            // display name must be built from the base (suffix stripped),
            // otherwise fwstate_rx_packets and fwstate_rx_bytes get different
            // keys and end up on two separate rows instead of one.
            let (base, is_packets, is_bytes) = if let Some(b) = stripped.strip_suffix("_packets") {
                (b, true, false)
            } else if let Some(b) = stripped.strip_suffix("_bytes") {
                (b, false, true)
            } else {
                (stripped, false, false)
            };

            let display = if counter_label.is_empty() {
                metrics::metric_display_name(base, "fwstate_")
            } else {
                metrics::metric_display_name(counter_label, "fwstate_")
            };
            let pair_key = format!("{base}\0{counter_label}");

            let pair = pair_map.entry(pair_key.clone()).or_insert_with(|| {
                pair_order.push(pair_key.clone());
                CounterPair {
                    display,
                    packets: None,
                    bytes: None,
                    entries: None,
                }
            });

            if is_packets {
                pair.packets = Some(val);
            } else if is_bytes {
                pair.bytes = Some(val);
            } else {
                // State-table entry counters (e.g. sync insert counters) and
                // any unsuffixed counter count frames/items, not
                // packets/bytes; render under Entries.
                pair.entries = Some(val);
            }
        }

        let rows: Vec<CounterRow> = pair_order
            .iter()
            .map(|pair_key| {
                let p = &pair_map[pair_key];
                CounterRow {
                    counter: p.display.clone(),
                    packets: p.packets.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    bytes: p.bytes.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    entries: p.entries.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                }
            })
            .collect();
        print_table_from_entries(rows);
        println!();
    }

    for cfg in &gauge_keys {
        let gauges = &gauge_map[cfg];
        let parts: Vec<&str> = cfg.split('\0').collect();
        let (config, af) = (parts[0], parts[1]);
        println!("FWSTATE MAP STATS  config={config} af={af}");
        println!();
        let rows: Vec<GaugeRow> = gauges
            .iter()
            .map(|m| GaugeRow {
                metric: metrics::metric_display_name(&m.name, "fwstate_"),
                value: metrics::format_gauge_value(&m.name, m.value.unwrap_or(0.0)),
            })
            .collect();
        print_table_from_entries(rows);
        println!();
    }

    metrics::print_grpc_metrics(&grpc_counters, &grpc_histograms);
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = FWStateService::new(&cmd.connection).await?;
    let format = cmd.format;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Link(cmd) => service.link_fwstate(cmd).await,
        ModeCmd::Stats(cmd) => service.get_stats(cmd).await,
        ModeCmd::Entries(cmd) => service.list_entries(cmd, format).await,
        ModeCmd::Metrics(cmd) => service.metrics(cmd).await,
        ModeCmd::Map { command } => service.manage_maps(command).await,
    }
}

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

/// Completion candidates for a `--name` argument: the fwstate configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }

    #[test]
    fn format_endpoint_brackets_ipv6_only() {
        let v4: IpAddress = "192.0.2.10".parse().unwrap();
        let v6: IpAddress = "2001:db8::2".parse().unwrap();

        assert_eq!("192.0.2.10:10000", format_endpoint(Some(&v4), 10000));
        assert_eq!("[2001:db8::2]:80", format_endpoint(Some(&v6), 80));
    }

    #[test]
    fn format_endpoint_leaves_ipv4_mapped_bare() {
        let mapped: IpAddress = "::ffff:192.0.2.10".parse().unwrap();

        assert_eq!("192.0.2.10:80", format_endpoint(Some(&mapped), 80));
    }

    #[test]
    fn format_endpoint_renders_absent_and_malformed() {
        let malformed = IpAddress { addr: vec![0u8; 5] };

        assert_eq!("?:0", format_endpoint(None, 0));
        assert_eq!("invalid:80", format_endpoint(Some(&malformed), 80));
    }
}
