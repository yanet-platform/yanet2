use core::{fmt, net::IpAddr};

use args::{CreateCmd, DeleteCmd, DirectionArg, EntriesCmd, InsertLayerCmd, ListCmd, ModeCmd, StatsCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{CompleteEnv, engine::CompletionCandidate};
use commonpb::pb::IpAddress;
use fwstatemappb::{
    CreateMapRequest, DeleteMapRequest, Direction, GetMapStatsRequest, InsertLayerRequest, Kind, ListEntriesRequest,
    ListMapsRequest, fw_state_map_service_client::FwStateMapServiceClient,
};
use serde::Serialize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatemappb {
    use serde::Serialize;

    tonic::include_proto!("objects.fwstate.controlplane.fwstatemappb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "objects.fwstate.controlplane.fwstatemappb.v1.FWStateMapService";

/// FWState-map CLI: manages the standalone fwstate-map objects module
/// configs (fwstate sync, ACL) link by name.
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

pub struct FWStateMapService {
    service: Service<FwStateMapServiceClient<LayeredChannel>>,
}

/// State an `entries` dump carries across its batches.
struct DumpState {
    /// Entries printed so far, which `--count` limits.
    printed: u32,
    /// Whether the human-readable header row is already out. Deferred until
    /// the first entry arrives, so a zero-entry result prints no header.
    header_printed: bool,
    /// Map generation the last response reported.
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
                "fwstate-map changed mid-dump (generation {previous} -> {generation}): \
                 entries may repeat or be missed"
            ),
            _ => {}
        }
    }
}

impl FWStateMapService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            FwStateMapServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });

        Ok(Self { service })
    }

    pub async fn map_create(&mut self, cmd: CreateCmd) -> Result<(), Error> {
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
        self.service
            .client()
            .create_map(request)
            .await
            .map_err(self.service.status("create"))?;

        output::success("create", format_args!("Created fwstate-map {}.", cmd.map_name));

        Ok(())
    }

    pub async fn map_delete(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteMapRequest { name: cmd.map_name.clone() };
        log::trace!("DeleteMapRequest: {request:?}");
        self.service
            .client()
            .delete_map(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted fwstate-map {}.", cmd.map_name));

        Ok(())
    }

    pub async fn map_list(&mut self, _cmd: ListCmd) -> Result<(), Error> {
        let request = ListMapsRequest {};
        let response = self
            .service
            .client()
            .list_maps(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        // The wire response's kinds map has no defined iteration order and
        // HashMap serialization is keyed by name only, so a stable payload
        // pairs each name with its family in the listing's own order. A
        // kind the enum does not know (a server ahead of this CLI) reads
        // as "unknown" rather than silently pretending to be a family.
        #[derive(Serialize)]
        struct ListedMap<'a> {
            name: &'a str,
            kind: &'a str,
        }
        let listed: Vec<ListedMap> = response
            .maps
            .iter()
            .map(|name| ListedMap {
                name,
                kind: match response.kinds.get(name).and_then(|raw| Kind::try_from(*raw).ok()) {
                    Some(Kind::V4) => "v4",
                    Some(Kind::V6) => "v6",
                    // Absent from the map or a discriminant this CLI's
                    // enum does not know (a server ahead of it).
                    None => "unknown",
                },
            })
            .collect();

        output::data(
            || &listed,
            || {
                if response.maps.is_empty() {
                    output::empty_with_hint(
                        format_args!("No fwstate-map objects found."),
                        format_args!("create one with 'yanet-cli-fwstatemap create --name <name> --kind <v4|v6>'"),
                    );
                    return;
                }

                // The human render keeps the plain name list; the family
                // pairs above are the structured payload for JSON output.
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response.maps)
                        .expect("fwstate-map list JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn map_stats(&mut self, cmd: StatsCmd) -> Result<(), Error> {
        let request = GetMapStatsRequest { name: cmd.map_name.clone() };
        log::trace!("GetMapStatsRequest: {request:?}");
        let response = self
            .service
            .client()
            .get_map_stats(request)
            .await
            .map_err(self.service.status("stats"))?
            .into_inner();

        output::data(
            || &response,
            || {
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response)
                        .expect("fwstate-map stats JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn map_insert_layer(&mut self, cmd: InsertLayerCmd) -> Result<(), Error> {
        // The map object carries its own address family; the request's kind
        // field is redundant with it, so it is left at the default.
        let request = InsertLayerRequest {
            name: cmd.map_name.clone(),
            index_size: cmd.index_size.unwrap_or(0),
            extra_bucket_count: cmd.extra_bucket_count.unwrap_or(0),
            worker_count: cmd.worker_count.unwrap_or(0),
            ..Default::default()
        };
        log::trace!("InsertLayerRequest: {request:?}");
        self.service
            .client()
            .insert_layer(request)
            .await
            .map_err(self.service.status("insert-layer"))?;

        output::success(
            "insert-layer",
            format_args!("Inserted layer into fwstate-map {}.", cmd.map_name),
        );

        Ok(())
    }

    pub async fn map_entries(&mut self, cmd: EntriesCmd, format: CommonFormat) -> Result<(), Error> {
        let direction = match cmd.direction {
            DirectionArg::Forward => Direction::Forward,
            DirectionArg::Backward => Direction::Backward,
        };

        let limit = cmd.count;
        let mut state = DumpState::new();

        // Plain cursor pagination: each request carries the full cursor
        // and the response's index feeds the next call until has_more is
        // false, so a failed page is one failed call rather than a torn
        // stream.
        let mut index = cmd.index as i64;
        loop {
            let resp = self
                .service
                .client()
                .list_entries(ListEntriesRequest {
                    map_name: cmd.map_name.clone(),
                    layer_index: cmd.layer,
                    include_expired: cmd.include_expired,
                    direction: direction as i32,
                    batch_size: cmd.batch,
                    index,
                })
                .await
                .map_err(self.service.status("entries"))?
                .into_inner();

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
                            serde_json::to_string(entry).expect("fwstate-map entry JSON serialization must not fail")
                        );
                    }
                }

                state.printed += 1;
            }

            if (limit > 0 && state.printed >= limit) || !resp.has_more {
                break;
            }
            index = resp.index;
        }

        if state.printed == 0 {
            output::empty(format_args!(
                "No firewall state entries found for map '{}'.",
                cmd.map_name
            ));
        }

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

fn print_entry(entry: &fwstatemappb::FwStateEntry) {
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

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = FWStateMapService::new(&cmd.connection).await?;
    let format = cmd.format;

    match cmd.mode {
        ModeCmd::List => service.map_list(args::ListCmd).await,
        ModeCmd::Create(cmd) => service.map_create(cmd).await,
        ModeCmd::Delete(cmd) => service.map_delete(cmd).await,
        ModeCmd::Stats(cmd) => service.map_stats(cmd).await,
        ModeCmd::Entries(cmd) => service.map_entries(cmd, format).await,
        ModeCmd::InsertLayer(cmd) => service.map_insert_layer(cmd).await,
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

/// Completion candidates for a `--name` argument: the fwstate-map objects
/// the map service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn map_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            FwStateMapServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_maps(ListMapsRequest {}).await?.into_inner().maps),
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
