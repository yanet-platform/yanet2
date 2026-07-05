use core::{fmt, net::Ipv6Addr, time::Duration};
use std::time::UNIX_EPOCH;

use args::{DeleteCmd, DirectionArg, EntriesCmd, LinkCmd, ModeCmd, ShowCmd, StatsCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::IpAddress;
use fwstatepb::{
    DeleteConfigRequest, Direction, GetStatsRequest, LinkFwStateRequest, ListConfigsRequest, ListEntriesRequest,
    ShowConfigRequest, UpdateConfigRequest, fw_state_service_client::FwStateServiceClient,
};
use netip::MacAddr;
use serde::Serialize;
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Status, codec::CompressionEncoding};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

mod args;

#[allow(non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("modules.fwstate.controlplane.fwstatepb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.FWStateService";

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

/// Parse a MAC address string.
fn parse_mac(s: &str) -> Result<MacAddr, String> {
    s.parse::<MacAddr>().map_err(|err| err.to_string())
}

pub struct FWStateService {
    service: Service<FwStateServiceClient<LayeredChannel>>,
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
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
            &response.configs,
            response.configs.is_empty(),
            format_args!("no fwstate configs"),
            || {
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

        output::data(&response, false, format_args!(""), || {
            println!(
                "{}",
                serde_json::to_string_pretty(&response).expect("fwstate config JSON serialization must not fail")
            );
        });

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

        if let Some(ref dst_ether) = cmd.dst_ether {
            let mac = parse_mac(dst_ether).map_err(|err| self.service.invalid("update", err))?;
            sync_config.dst_ether = Some(mac.into());
        }

        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast =
                Some(parse_ipv6(dst_addr_multicast).map_err(|err| self.service.invalid("update", err))?);
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = port_multicast;
        }

        if let Some(ref dst_addr_unicast) = cmd.dst_addr_unicast {
            sync_config.dst_addr_unicast =
                Some(parse_ipv6(dst_addr_unicast).map_err(|err| self.service.invalid("update", err))?);
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

        output::data(&response, false, format_args!(""), || {
            println!(
                "{}",
                serde_json::to_string_pretty(&response).expect("fwstate stats JSON serialization must not fail")
            );
        });

        Ok(())
    }

    pub async fn list_entries(&mut self, cmd: EntriesCmd, format: CommonFormat) -> Result<(), Error> {
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

        let limit = cmd.count;
        let mut total: u32 = 0;

        if format == CommonFormat::Human {
            println!(
                "{:<6} {:<45} {:<45} {:<8} {:<9} {:<7}",
                "IDX", "SRC", "DST", "PROTO", "FLAGS S|D", "EXPRD"
            );
        }

        while let Some(resp) = response_stream
            .message()
            .await
            .map_err(self.service.status("list entries"))?
        {
            for entry in &resp.entries {
                if limit > 0 && total >= limit {
                    break;
                }

                match format {
                    CommonFormat::Human => print_entry(entry),
                    CommonFormat::Json => {
                        let json_entry = JsonEntry::from_entry(entry);
                        println!(
                            "{}",
                            serde_json::to_string(&json_entry).expect("fwstate entry JSON serialization must not fail")
                        );
                    }
                }

                total += 1;
            }

            if (limit > 0 && total >= limit) || !resp.has_more {
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
            tx.send(next_req)
                .await
                .map_err(|err| self.service.status("list entries")(Status::internal(format!("send error: {err}"))))?;
        }

        Ok(())
    }
}

fn format_addr(addr: Option<&IpAddress>) -> String {
    addr.map(|a| a.to_string()).unwrap_or_else(|| "?".to_string())
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
    fn from_entry(entry: &fwstatepb::FwStateEntry) -> Self {
        let key = entry.key.as_ref();
        let val = entry.value.as_ref();
        let flags = FwStateFlags(val.map(|v| v.flags).unwrap_or(0));
        let external = val.map(|v| v.external).unwrap_or(false);

        Self {
            idx: entry.idx,
            expired: entry.expired,
            src_port: key.map(|k| k.src_port).unwrap_or(0),
            dst_port: key.map(|k| k.dst_port).unwrap_or(0),
            src_addr: format_addr(key.and_then(|k| k.src_addr.as_ref())),
            dst_addr: format_addr(key.and_then(|k| k.dst_addr.as_ref())),
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

fn print_entry(entry: &fwstatepb::FwStateEntry) {
    let (src_addr, dst_addr, src_port, dst_port, proto) = match &entry.key {
        Some(k) => (
            format_addr(k.src_addr.as_ref()),
            format_addr(k.dst_addr.as_ref()),
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
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}
