use core::error::Error;
use std::{
    fmt,
    net::{Ipv4Addr, Ipv6Addr},
    time::{Duration, UNIX_EPOCH},
};

use args::{DeleteCmd, DirectionArg, EntriesCmd, LinkCmd, ModeCmd, ShowCmd, StatsCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use fwstatepb::{
    DeleteConfigRequest, Direction, GetStatsRequest, LinkFwStateRequest, ListConfigsRequest, ListEntriesRequest,
    ShowConfigRequest, UpdateConfigRequest, fw_state_service_client::FwStateServiceClient,
};
use serde::Serialize;
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

mod args;

#[allow(non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("fwstatepb");
}

/// FWState module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Parse IPv6 address from string to bytes
fn parse_ipv6(s: &str) -> Result<Vec<u8>, Box<dyn Error>> {
    let addr: Ipv6Addr = s.parse()?;
    Ok(addr.octets().to_vec())
}

/// Parse MAC address from string to bytes
fn parse_mac(s: &str) -> Result<Vec<u8>, Box<dyn Error>> {
    let parts: Vec<&str> = s.split(':').collect();
    if parts.len() != 6 {
        return Err(format!("invalid MAC address format: {}", s).into());
    }

    let mut bytes = Vec::with_capacity(6);
    for part in parts {
        let byte = u8::from_str_radix(part, 16).map_err(|_| format!("invalid MAC address byte: {}", part))?;
        bytes.push(byte);
    }

    Ok(bytes)
}

pub struct FWStateService {
    client: FwStateServiceClient<LayeredChannel>,
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = FwStateServiceClient::new(channel)
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
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: false,
        };
        let response = self.client.show_config(request).await?.into_inner();
        println!("{}", serde_json::to_string(&response)?);
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client.delete_config(request).await?.into_inner();
        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        // First, fetch the current config to merge with new values
        let current_request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: true,
        };
        let current_response = self.client.show_config(current_request).await;
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
            sync_config.src_addr = parse_ipv6(src_addr)?;
        }

        if let Some(ref dst_ether) = cmd.dst_ether {
            sync_config.dst_ether = parse_mac(dst_ether)?;
        }

        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast = parse_ipv6(dst_addr_multicast)?;
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = port_multicast;
        }

        if let Some(ref dst_addr_unicast) = cmd.dst_addr_unicast {
            sync_config.dst_addr_unicast = parse_ipv6(dst_addr_unicast)?;
        }

        if let Some(port_unicast) = cmd.port_unicast {
            sync_config.port_unicast = port_unicast;
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

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            map_config: Some(map_config),
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");
        Ok(())
    }

    pub async fn link_fwstate(&mut self, cmd: LinkCmd) -> Result<(), Box<dyn Error>> {
        let request = LinkFwStateRequest {
            fwstate_name: cmd.config_name.clone(),
            acl_config_names: cmd.acl_configs.clone(),
        };
        log::trace!("LinkFwStateRequest: {request:?}");
        let response = self.client.link_fw_state(request).await?.into_inner();
        log::debug!("LinkFwStateResponse: {response:?}");
        Ok(())
    }

    pub async fn get_stats(&mut self, cmd: StatsCmd) -> Result<(), Box<dyn Error>> {
        let request = GetStatsRequest { name: cmd.config_name.clone() };
        log::trace!("GetStatsRequest: {request:?}");
        let response = self.client.get_stats(request).await?.into_inner();
        println!("{}", serde_json::to_string_pretty(&response)?);
        Ok(())
    }

    pub async fn list_entries(&mut self, cmd: EntriesCmd) -> Result<(), Box<dyn Error>> {
        let direction = match cmd.direction {
            DirectionArg::Forward => Direction::Forward,
            DirectionArg::Backward => Direction::Backward,
        };

        let (tx, rx) = mpsc::channel(1);
        let stream = ReceiverStream::new(rx);

        let initial_req = ListEntriesRequest {
            config_name: cmd.config_name.clone(),
            is_ipv6: cmd.ipv6,
            layer_index: cmd.layer,
            include_expired: cmd.include_expired,
            direction: direction as i32,
            batch_size: cmd.batch,
            index: cmd.index as i64,
        };
        tx.send(initial_req).await.map_err(|e| format!("send error: {e}"))?;

        let mut response_stream = self.client.list_entries(stream).await?.into_inner();

        let json_output = cmd.json;
        let limit = cmd.count;
        let mut total: u32 = 0;

        if !json_output {
            println!(
                "{:<6} {:<45} {:<45} {:<8} {:<9} {:<7}",
                "IDX", "SRC", "DST", "PROTO", "FLAGS S|D", "EXPRD"
            );
        }

        loop {
            let resp = match response_stream.message().await? {
                Some(r) => r,
                None => break,
            };

            for entry in &resp.entries {
                if limit > 0 && total >= limit {
                    break;
                }
                if json_output {
                    let je = JsonEntry::from_entry(entry, cmd.ipv6);
                    println!("{}", serde_json::to_string(&je)?);
                } else {
                    print_entry(entry, cmd.ipv6);
                }
                total += 1;
            }

            if limit > 0 && total >= limit {
                break;
            }

            if !resp.has_more {
                break;
            }

            let next_req = ListEntriesRequest {
                config_name: cmd.config_name.clone(),
                is_ipv6: cmd.ipv6,
                layer_index: cmd.layer,
                include_expired: cmd.include_expired,
                direction: direction as i32,
                batch_size: cmd.batch,
                index: resp.index,
            };
            tx.send(next_req).await.map_err(|e| format!("send error: {e}"))?;
        }

        Ok(())
    }
}

fn format_addr(addr: Option<&fwstatepb::Addr>, is_ipv6: bool) -> String {
    match addr {
        Some(a) if is_ipv6 && a.bytes.len() == 16 => {
            let octets: [u8; 16] = a.bytes[..16].try_into().unwrap();
            Ipv6Addr::from(octets).to_string()
        }
        Some(a) if !is_ipv6 && a.bytes.len() == 4 => {
            let octets: [u8; 4] = a.bytes[..4].try_into().unwrap();
            Ipv4Addr::from(octets).to_string()
        }
        _ => "?".to_string(),
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

const TCP_FLAG_TABLE: [(u8, char, &str); 4] = [
    (0x08, 'A', "ACK"),
    (0x02, 'S', "SYN"),
    (0x04, 'R', "RST"),
    (0x01, 'F', "FIN"),
];

impl TcpNibble {
    fn names(&self) -> Vec<&'static str> {
        TCP_FLAG_TABLE
            .iter()
            .filter(|(mask, _, _)| self.0 & mask != 0)
            .map(|(_, _, name)| *name)
            .collect()
    }
}

impl fmt::Display for TcpNibble {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (mask, ch, _) in TCP_FLAG_TABLE {
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

/// Flat JSON representation of a firewall state entry.
#[derive(Serialize)]
struct JsonEntry {
    idx: u32,
    expired: bool,
    src_port: u32,
    dst_port: u32,
    src_addr: String,
    dst_addr: String,
    proto: String,
    origin: &'static str,
    flags: SrcDstFlags,
    packets: SrcDstPackets,
    created_at: String,
    updated_at: String,
}

#[derive(Serialize)]
struct SrcDstFlags {
    src: Vec<&'static str>,
    dst: Vec<&'static str>,
}

#[derive(Serialize)]
struct SrcDstPackets {
    src: u64,
    dst: u64,
}

impl JsonEntry {
    fn from_entry(entry: &fwstatepb::FwStateEntry, is_ipv6: bool) -> Self {
        let key = entry.key.as_ref();
        let val = entry.value.as_ref();
        let flags = FwStateFlags(val.map(|v| v.flags).unwrap_or(0));
        let external = val.map(|v| v.external).unwrap_or(false);

        Self {
            idx: entry.idx,
            expired: entry.expired,
            src_port: key.map(|k| k.src_port).unwrap_or(0),
            dst_port: key.map(|k| k.dst_port).unwrap_or(0),
            src_addr: format_addr(key.and_then(|k| k.src_addr.as_ref()), is_ipv6),
            dst_addr: format_addr(key.and_then(|k| k.dst_addr.as_ref()), is_ipv6),
            proto: format_proto(key.map(|k| k.proto).unwrap_or(0)),
            origin: if external { "external" } else { "local" },
            flags: SrcDstFlags {
                src: flags.src().names(),
                dst: flags.dst().names(),
            },
            packets: SrcDstPackets {
                src: val.map(|v| v.packets_forward).unwrap_or(0),
                dst: val.map(|v| v.packets_backward).unwrap_or(0),
            },
            created_at: humantime::format_rfc3339(
                UNIX_EPOCH + Duration::from_nanos(val.map(|v| v.created_at).unwrap_or(0)),
            )
            .to_string(),
            updated_at: humantime::format_rfc3339(
                UNIX_EPOCH + Duration::from_nanos(val.map(|v| v.updated_at).unwrap_or(0)),
            )
            .to_string(),
        }
    }
}

fn print_entry(entry: &fwstatepb::FwStateEntry, is_ipv6: bool) {
    let (src_addr, dst_addr, src_port, dst_port, proto) = match &entry.key {
        Some(k) => (
            format_addr(k.src_addr.as_ref(), is_ipv6),
            format_addr(k.dst_addr.as_ref(), is_ipv6),
            k.src_port,
            k.dst_port,
            k.proto,
        ),
        None => ("?".into(), "?".into(), 0, 0, 0),
    };

    let flags = entry.value.as_ref().map(|v| v.flags).unwrap_or(0);

    let src = format!("{}:{}", src_addr, src_port);
    let dst = format!("{}:{}", dst_addr, dst_port);

    println!(
        "{:<6} {:<45} {:<45} {:<8} {:<9} {:<7}",
        entry.idx,
        src,
        dst,
        format_proto(proto),
        FwStateFlags(flags),
        if entry.expired { "yes" } else { "no" },
    );
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = FWStateService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Link(cmd) => service.link_fwstate(cmd).await,
        ModeCmd::Stats(cmd) => service.get_stats(cmd).await,
        ModeCmd::Entries(cmd) => service.list_entries(cmd).await,
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
