use core::error::Error;
use std::{collections::HashMap, fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, GetMetricsRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    acl_service_client::AclServiceClient,
};
use args::{DeleteCmd, MetricsCmd, ModeCmd, OutputFormat, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use metric::Metric;
use netip::IpNetwork;
use serde::{Deserialize, Serialize};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    display::print_table,
    logging,
};

mod args;
mod metric;

mod commonpb {
    tonic::include_proto!("commonpb");
}

#[allow(non_snake_case)]
pub mod filterpb {
    tonic::include_proto!("filterpb");
}

#[allow(non_snake_case)]
pub mod aclpb {
    tonic::include_proto!("aclpb");
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Serialize)]
struct SerializableShowConfigResponse {
    name: String,
    rules: Vec<SerializableRule>,
}

#[derive(Serialize)]
struct SerializableRule {
    srcs: Vec<String>,
    dsts: Vec<String>,
    src_port_ranges: Vec<String>,
    dst_port_ranges: Vec<String>,
    proto_ranges: Vec<String>,
    vlan_ranges: Vec<String>,
    devices: Vec<String>,
    action: Option<SerializableAction>,
}

#[derive(Serialize)]
struct SerializableAction {
    counter: String,
    keep_state: bool,
    kind: i32,
}

#[derive(Tabled)]
struct CounterRow {
    #[tabled(rename = "Counter")]
    counter: String,
    #[tabled(rename = "Packets")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
}

#[derive(Tabled)]
struct GaugeRow {
    #[tabled(rename = "Metric")]
    metric: String,
    #[tabled(rename = "Value")]
    value: String,
}

#[derive(Tabled)]
struct HistRow {
    #[tabled(rename = "Handler")]
    handler: String,
    #[tabled(rename = "Total Calls")]
    total: String,
    #[tabled(rename = "P50")]
    p50: String,
    #[tabled(rename = "P95")]
    p95: String,
    #[tabled(rename = "P99")]
    p99: String,
}

fn print_counter_table(rows: Vec<CounterRow>) {
    let show_packets = rows.iter().any(|r| r.packets != "-");
    let show_bytes = rows.iter().any(|r| r.bytes != "-");

    if !show_packets && !show_bytes {
        return;
    }

    let mut builder = tabled::builder::Builder::new();
    let mut header = vec!["Counter".to_string()];
    if show_packets {
        header.push("Packets".to_string());
    }
    if show_bytes {
        header.push("Bytes".to_string());
    }
    builder.push_record(header);

    for r in rows {
        let mut row = vec![r.counter];
        if show_packets {
            row.push(r.packets);
        }
        if show_bytes {
            row.push(r.bytes);
        }
        builder.push_record(row);
    }

    let mut table = builder.build();
    ync::display::apply_style(&mut table);
    println!("{table}");
}

fn format_number(n: u64) -> String {
    let s = n.to_string();
    let mut result = String::new();
    for (i, c) in s.chars().rev().enumerate() {
        if i > 0 && i % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    result.chars().rev().collect()
}

fn format_gauge_value(name: &str, value: f64) -> String {
    if name.ends_with("_ns") {
        if value < 1_000.0 {
            format!("{:.0}ns", value)
        } else if value < 1_000_000.0 {
            format!("{:.2}µs", value / 1_000.0)
        } else if value < 1_000_000_000.0 {
            format!("{:.2}ms", value / 1_000_000.0)
        } else {
            format!("{:.2}s", value / 1_000_000_000.0)
        }
    } else if name.ends_with("_bytes") {
        if value < 1024.0 {
            format!("{:.0} B", value)
        } else if value < 1024.0 * 1024.0 {
            format!("{:.2} KB", value / 1024.0)
        } else if value < 1024.0 * 1024.0 * 1024.0 {
            format!("{:.2} MB", value / (1024.0 * 1024.0))
        } else {
            format!("{:.2} GB", value / (1024.0 * 1024.0 * 1024.0))
        }
    } else {
        format_number(value as u64)
    }
}

fn metric_display_name(name: &str) -> String {
    let stripped = name.strip_prefix("acl_").unwrap_or(name);
    stripped
        .split('_')
        .map(|word| {
            let mut c = word.chars();
            match c.next() {
                None => String::new(),
                Some(first) => first.to_uppercase().collect::<String>() + c.as_str(),
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

fn print_metrics_table(metrics: &[Metric]) {
    if metrics.is_empty() {
        println!("No metrics found.");
        return;
    }

    struct CounterPair {
        display: String,
        packets: Option<u64>,
        bytes: Option<u64>,
    }

    let mut location_keys: Vec<String> = Vec::new();
    let mut location_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut gauge_keys: Vec<String> = Vec::new();
    let mut gauge_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut histograms: Vec<&Metric> = Vec::new();

    for m in metrics {
        match m.kind {
            metric::Kind::Histogram => histograms.push(m),
            metric::Kind::Gauge => {
                let cfg = m.label_value("config").unwrap_or("global").to_string();
                if !gauge_map.contains_key(&cfg) {
                    gauge_keys.push(cfg.clone());
                }
                gauge_map.entry(cfg).or_default().push(m);
            }
            metric::Kind::Counter => {
                let key = format!(
                    "{}\0{}\0{}\0{}\0{}",
                    m.label_value("config").unwrap_or(""),
                    m.label_value("device").unwrap_or(""),
                    m.label_value("pipeline").unwrap_or(""),
                    m.label_value("function").unwrap_or(""),
                    m.label_value("chain").unwrap_or(""),
                );
                if !location_map.contains_key(&key) {
                    location_keys.push(key.clone());
                }
                location_map.entry(key).or_default().push(m);
            }
            metric::Kind::Unknown => {}
        }
    }

    for (loc_idx, key) in location_keys.iter().enumerate() {
        if loc_idx > 0 {
            println!();
        }
        let counters = &location_map[key];
        let parts: Vec<&str> = key.split('\0').collect();
        let (cfg, device, pipeline, function, chain) = (parts[0], parts[1], parts[2], parts[3], parts[4]);
        println!("ACL COUNTERS  config={cfg} device={device} pipeline={pipeline} function={function} chain={chain}");
        println!();

        let std_counters: Vec<&&Metric> = counters.iter().filter(|m| m.label_value("counter").is_none()).collect();
        let rule_counters: Vec<&&Metric> = counters.iter().filter(|m| m.label_value("counter").is_some()).collect();

        let mut pair_order: Vec<String> = Vec::new();
        let mut pair_map: HashMap<String, CounterPair> = HashMap::new();

        for m in &std_counters {
            let val = m.value.unwrap_or(0.0) as u64;
            let stripped = m.name.strip_prefix("acl_").unwrap_or(&m.name);
            if let Some(base) = stripped.strip_suffix("_packets") {
                let pair = pair_map.entry(base.to_string()).or_insert_with(|| {
                    pair_order.push(base.to_string());
                    CounterPair {
                        display: metric_display_name(base),
                        packets: None,
                        bytes: None,
                    }
                });
                pair.packets = Some(val);
            } else if let Some(base) = stripped.strip_suffix("_bytes") {
                let pair = pair_map.entry(base.to_string()).or_insert_with(|| {
                    pair_order.push(base.to_string());
                    CounterPair {
                        display: metric_display_name(base),
                        packets: None,
                        bytes: None,
                    }
                });
                pair.bytes = Some(val);
            }
        }

        if !pair_order.is_empty() {
            let rows: Vec<CounterRow> = pair_order
                .iter()
                .map(|k| {
                    let p = &pair_map[k];
                    CounterRow {
                        counter: p.display.clone(),
                        packets: p.packets.map(format_number).unwrap_or_else(|| "-".into()),
                        bytes: p.bytes.map(format_number).unwrap_or_else(|| "-".into()),
                    }
                })
                .collect();
            print_counter_table(rows);
        }

        if !rule_counters.is_empty() {
            println!();
            println!("Per-Rule Counters:");

            let mut rule_order: Vec<String> = Vec::new();
            let mut rule_map_inner: HashMap<String, (Option<u64>, Option<u64>)> = HashMap::new();

            for m in &rule_counters {
                let rule_name = m.label_value("counter").unwrap_or("unknown").to_string();
                let val = m.value.unwrap_or(0.0) as u64;
                if !rule_map_inner.contains_key(&rule_name) {
                    rule_order.push(rule_name.clone());
                    rule_map_inner.insert(rule_name.clone(), (None, None));
                }
                let entry = rule_map_inner.get_mut(&rule_name).unwrap();
                if m.name.ends_with("_packets") {
                    entry.0 = Some(val);
                } else if m.name.ends_with("_bytes") {
                    entry.1 = Some(val);
                }
            }

            let rows: Vec<CounterRow> = rule_order
                .iter()
                .map(|name| {
                    let (pkts, b) = rule_map_inner[name];
                    CounterRow {
                        counter: name.clone(),
                        packets: pkts.map(format_number).unwrap_or_else(|| "-".into()),
                        bytes: b.map(format_number).unwrap_or_else(|| "-".into()),
                    }
                })
                .collect();
            print_counter_table(rows);
        }

        println!();
    }

    for cfg in &gauge_keys {
        let gauges = &gauge_map[cfg];
        println!("ACL CONFIG INFO  config={cfg}");
        println!();
        let rows: Vec<GaugeRow> = gauges
            .iter()
            .map(|m| GaugeRow {
                metric: metric_display_name(&m.name),
                value: format_gauge_value(&m.name, m.value.unwrap_or(0.0)),
            })
            .collect();
        print_table(rows);
        println!();
    }

    if !histograms.is_empty() {
        println!("ACL HANDLER LATENCIES");
        println!();
        let rows: Vec<HistRow> = histograms
            .iter()
            .map(|m| {
                let handler = m.label_value("handler").unwrap_or("unknown").to_string();
                match &m.histogram {
                    Some(h) => HistRow {
                        handler,
                        total: format_number(h.total_count),
                        p50: metric::histogram_percentile(&h.buckets, h.total_count, 50.0),
                        p95: metric::histogram_percentile(&h.buckets, h.total_count, 95.0),
                        p99: metric::histogram_percentile(&h.buckets, h.total_count, 99.0),
                    },
                    None => HistRow {
                        handler,
                        total: "-".into(),
                        p50: "-".into(),
                        p95: "-".into(),
                        p99: "-".into(),
                    },
                }
            })
            .collect();
        print_table(rows);
    }
}

////////////////////////////////////////////////////////////////////////////////

/// ACL module
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

impl TryFrom<&String> for filterpb::IpNet {
    type Error = Box<dyn Error>;

    fn try_from(value: &String) -> Result<Self, Self::Error> {
        let net = IpNetwork::parse(value)?;
        match net {
            IpNetwork::V4(net) => Ok(filterpb::IpNet {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            }),
            IpNetwork::V6(net) => Ok(filterpb::IpNet {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            }),
        }
    }
}

fn format_ip_net(net: &filterpb::IpNet) -> String {
    let prefix_len: u32 = net.mask.iter().map(|b| b.count_ones()).sum();
    if let Ok(arr) = <[u8; 4]>::try_from(net.addr.as_slice()) {
        format!("{}/{}", std::net::Ipv4Addr::from(arr), prefix_len)
    } else if let Ok(arr) = <[u8; 16]>::try_from(net.addr.as_slice()) {
        format!("{}/{}", std::net::Ipv6Addr::from(arr), prefix_len)
    } else {
        format!("{:?}/{}", net.addr, prefix_len)
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Range {
    from: u16,
    to: u16,
}

impl TryFrom<&Range> for filterpb::PortRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
        if r.from > r.to {
            return Err(format!("port 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&Range> for filterpb::ProtoRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
        if r.from > r.to {
            return Err(format!("protocol 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&Range> for filterpb::VlanRange {
    type Error = Box<dyn Error>;

    fn try_from(r: &Range) -> Result<Self, Self::Error> {
        if r.from > 4095 {
            return Err(format!("VLAN 'from' value {} exceeds maximum 4095", r.from).into());
        }
        if r.to > 4095 {
            return Err(format!("VLAN 'to' value {} exceeds maximum 4095", r.to).into());
        }
        if r.from > r.to {
            return Err(format!("VLAN 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
        }
        Ok(Self { from: r.from as u32, to: r.to as u32 })
    }
}

impl TryFrom<&String> for filterpb::Device {
    type Error = Box<dyn Error>;

    fn try_from(n: &String) -> Result<Self, Self::Error> {
        Ok(Self { name: n.to_string() })
    }
}

#[derive(Debug, Serialize, Deserialize)]
enum ActionKind {
    Allow,
    Deny,
    Count,
    CheckState,
    CreateState,
}

#[derive(Debug, Serialize, Deserialize)]
struct ACLRule {
    srcs: Vec<String>,
    dsts: Vec<String>,
    src_ports: Vec<Range>,
    dst_ports: Vec<Range>,
    proto_ranges: Vec<Range>,
    vlan_ranges: Vec<Range>,
    devices: Vec<String>,
    counter: String,
    action: ActionKind,
}

impl TryFrom<ACLRule> for aclpb::Rule {
    type Error = Box<dyn Error>;

    fn try_from(acl_rule: ACLRule) -> Result<Self, Self::Error> {
        Ok(Self {
            srcs: acl_rule
                .srcs
                .iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<_, _>>()?,
            dsts: acl_rule
                .dsts
                .iter()
                .map(filterpb::IpNet::try_from)
                .collect::<Result<_, _>>()?,
            vlan_ranges: acl_rule
                .vlan_ranges
                .iter()
                .map(filterpb::VlanRange::try_from)
                .collect::<Result<_, _>>()?,
            src_port_ranges: acl_rule
                .src_ports
                .iter()
                .map(filterpb::PortRange::try_from)
                .collect::<Result<_, _>>()?,
            dst_port_ranges: acl_rule
                .dst_ports
                .iter()
                .map(filterpb::PortRange::try_from)
                .collect::<Result<_, _>>()?,
            proto_ranges: acl_rule
                .proto_ranges
                .iter()
                .map(filterpb::ProtoRange::try_from)
                .collect::<Result<_, _>>()?,
            devices: acl_rule
                .devices
                .iter()
                .map(filterpb::Device::try_from)
                .collect::<Result<_, _>>()?,
            action: Some(aclpb::Action {
                counter: acl_rule.counter,
                keep_state: false,
                kind: match acl_rule.action {
                    ActionKind::Allow => aclpb::ActionKind::Pass,
                    ActionKind::Deny => aclpb::ActionKind::Deny,
                    ActionKind::Count => aclpb::ActionKind::Count,
                    ActionKind::CheckState => aclpb::ActionKind::CheckState,
                    ActionKind::CreateState => aclpb::ActionKind::CreateState,
                }
                .into(),
            }),
        })
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct ACLConfig {
    rules: Vec<ACLRule>,
}

impl TryFrom<ACLConfig> for Vec<aclpb::Rule> {
    type Error = Box<dyn Error>;

    fn try_from(config: ACLConfig) -> Result<Self, Self::Error> {
        config
            .rules
            .into_iter()
            .enumerate()
            .map(|(i, rule)| {
                rule.try_into().map_err(|e: Box<dyn Error>| -> Box<dyn Error> {
                    format!("failed to parse rule #{}: {}", i + 1, e).into()
                })
            })
            .collect()
    }
}

impl ACLConfig {
    pub fn from_file<P>(path: P) -> Result<Self, Box<dyn Error>>
    where
        P: AsRef<Path>,
    {
        let rd = File::open(path)?;
        Ok(serde_yaml::from_reader(rd)?)
    }
}

////////////////////////////////////////////////////////////////////////////////

pub struct ACLService {
    client: AclServiceClient<LayeredChannel>,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = AclServiceClient::new(channel)
            .max_decoding_message_size(256 * 1024 * 1024)
            .max_encoding_message_size(256 * 1024 * 1024)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response.configs)?);
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self.client.show_config(request).await?.into_inner();

        let out = SerializableShowConfigResponse {
            name: response.name,
            rules: response
                .rules
                .into_iter()
                .map(|rule| SerializableRule {
                    srcs: rule.srcs.iter().map(format_ip_net).collect(),
                    dsts: rule.dsts.iter().map(format_ip_net).collect(),
                    src_port_ranges: rule
                        .src_port_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    dst_port_ranges: rule
                        .dst_port_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    proto_ranges: rule
                        .proto_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    vlan_ranges: rule
                        .vlan_ranges
                        .iter()
                        .map(|r| format!("{}-{}", r.from, r.to))
                        .collect(),
                    devices: rule.devices.iter().map(|d| d.name.clone()).collect(),
                    action: rule.action.map(|a| SerializableAction {
                        counter: a.counter,
                        keep_state: a.keep_state,
                        kind: a.kind,
                    }),
                })
                .collect(),
        };
        println!("{}", serde_json::to_string(&out)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = ACLConfig::from_file(&cmd.rules)?;
        let rules: Vec<aclpb::Rule> = config.try_into()?;
        let request = UpdateConfigRequest { name: cmd.config_name.clone(), rules };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");
        Ok(())
    }

    pub async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Box<dyn Error>> {
        let response = self.client.get_metrics(GetMetricsRequest {}).await?.into_inner();

        let label_filters: Vec<(&str, &str)> = cmd
            .labels
            .iter()
            .filter_map(|s| {
                let mut it = s.splitn(2, '=');
                Some((it.next()?, it.next()?))
            })
            .collect();

        let metrics: Vec<Metric> = response
            .metrics
            .into_iter()
            .map(Metric::from_proto)
            .filter(|m| {
                if let Some(ref f) = cmd.name {
                    if !m.name.contains(f.as_filter()) {
                        return false;
                    }
                }
                label_filters.iter().all(|(k, v)| m.label_value(k) == Some(v))
            })
            .collect();

        match cmd.format {
            OutputFormat::Json => println!("{}", serde_json::to_string(&metrics)?),
            OutputFormat::Table => print_metrics_table(&metrics),
        }

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ACLService::new(&cmd.connection).await?;
    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Metrics(cmd) => service.metrics(cmd).await,
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
