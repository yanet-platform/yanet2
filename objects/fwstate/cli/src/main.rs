use core::{fmt, net::IpAddr};

use args::{CreateCmd, DeleteCmd, DirectionArg, EntriesCmd, InsertLayerCmd, ListCmd, ModeCmd, StatsCmd};
use clap::{CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use commonpb::pb::IpAddress;
use fwstatemappb::{
    CreateMapRequest, DeleteMapRequest, Direction, GetMapStatsRequest, InsertLayerRequest, Kind, ListEntriesRequest,
    ListMapsRequest, fw_state_map_service_client::FwStateMapServiceClient,
};
use serde::Serialize;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatemappb {
    tonic::include_proto!("objects.fwstate.controlplane.fwstatemappb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "objects.fwstate.controlplane.fwstatemappb.v1.FWStateMapService";

fn client(channel: LayeredChannel) -> FwStateMapServiceClient<LayeredChannel> {
    FwStateMapServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages fwstate-map objects that fwstate and acl configs link by name.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

type FWStateMapService = Service<FwStateMapServiceClient<LayeredChannel>>;

/// Warns when a response reports a different generation than the one
/// before it, `seen` carrying the last one across the batches.
///
/// A bump means layers were relinked, so the cursor and `--layer` stop
/// denoting what they did when the dump began, and the remaining
/// entries can repeat or be missed. Rows already printed were accurate
/// when read, so the dump goes on and only warns.
fn note_generation(seen: &mut Option<u64>, generation: u64) {
    match seen.replace(generation) {
        Some(previous) if previous != generation => log::warn!(
            "fwstate-map changed mid-dump (generation {previous} -> {generation}): \
             entries may repeat or be missed"
        ),
        _ => {}
    }
}

async fn map_create(service: &mut FWStateMapService, cmd: CreateCmd) -> Result<(), Error> {
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
    service
        .unary("create", request, async |client, request| {
            client.create_map(request).await
        })
        .await?;

    output::success("create", format_args!("Created map '{}'.", cmd.map_name));

    Ok(())
}

async fn map_delete(service: &mut FWStateMapService, cmd: DeleteCmd) -> Result<(), Error> {
    let request = DeleteMapRequest { name: cmd.map_name.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("map '{}'", cmd.map_name)),
            async |client, request| client.delete_map(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted map '{}'.", cmd.map_name));

    Ok(())
}

async fn map_list(service: &mut FWStateMapService, _cmd: ListCmd) -> Result<(), Error> {
    let response = service
        .unary("list", ListMapsRequest {}, async |client, request| {
            client.list_maps(request).await
        })
        .await?;

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
            display::print_names_with_hint(
                &response.maps,
                format_args!("No fwstate-map objects found."),
                format_args!("create one with 'yanet-cli-fwstatemap create --name <name> --kind <v4|v6>'"),
            )
        },
    );

    Ok(())
}

async fn map_stats(service: &mut FWStateMapService, cmd: StatsCmd) -> Result<(), Error> {
    let request = GetMapStatsRequest { name: cmd.map_name.clone() };
    let response = service
        .unary_with(
            "stats",
            request,
            service.not_found("stats", &format!("map '{}'", cmd.map_name)),
            async |client, request| client.get_map_stats(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            println!(
                "{}",
                serde_json::to_string_pretty(&response).expect("fwstate-map stats JSON serialization must not fail")
            );
        },
    );

    Ok(())
}

async fn map_insert_layer(service: &mut FWStateMapService, cmd: InsertLayerCmd) -> Result<(), Error> {
    // The map object carries its own address family; the request's kind
    // field is redundant with it, so it is left at the default.
    let request = InsertLayerRequest {
        name: cmd.map_name.clone(),
        index_size: cmd.index_size.unwrap_or(0),
        extra_bucket_count: cmd.extra_bucket_count.unwrap_or(0),
        worker_count: cmd.worker_count.unwrap_or(0),
        ..Default::default()
    };
    service
        .unary("insert-layer", request, async |client, request| {
            client.insert_layer(request).await
        })
        .await?;

    output::success(
        "insert-layer",
        format_args!("Inserted layer into map '{}'.", cmd.map_name),
    );

    Ok(())
}

async fn map_entries(service: &mut FWStateMapService, cmd: EntriesCmd) -> Result<(), Error> {
    let direction = match cmd.direction {
        DirectionArg::Forward => Direction::Forward,
        DirectionArg::Backward => Direction::Backward,
    };

    let limit = usize::try_from(cmd.count).expect("a count fits usize");
    let mut generation = None;
    let mut rows = output::rows(print_entries_header, print_entry);

    // Plain cursor pagination: each request carries the full cursor
    // and the response's index feeds the next call until has_more is
    // false, so a failed page is one failed call rather than a torn
    // stream.
    let mut index = cmd.index as i64;
    loop {
        let request = ListEntriesRequest {
            map_name: cmd.map_name.clone(),
            layer_index: cmd.layer,
            include_expired: cmd.include_expired,
            direction: direction as i32,
            batch_size: cmd.batch,
            index,
        };
        let resp = service
            .unary_with(
                "entries",
                request,
                service.not_found("entries", &format!("map '{}'", cmd.map_name)),
                async |client, request| client.list_entries(request).await,
            )
            .await?;

        note_generation(&mut generation, resp.generation);

        for entry in &resp.entries {
            if limit > 0 && rows.printed() >= limit {
                break;
            }

            rows.push(entry);
        }

        if (limit > 0 && rows.printed() >= limit) || !resp.has_more {
            break;
        }
        index = resp.index;
    }

    rows.finish(format_args!(
        "No firewall state entries found for map '{}'.",
        cmd.map_name
    ));

    Ok(())
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

    match IpAddr::try_from(addr).map(|addr| addr.to_canonical()) {
        Ok(IpAddr::V4(v4)) => format!("{v4}:{port}"),
        Ok(IpAddr::V6(v6)) => format!("[{v6}]:{port}"),
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

fn print_entries_header() {
    println!(
        "{:<6} {:<48} {:<48} {:<8} {:<9} {:<7}",
        "IDX", "SRC", "DST", "PROTO", "FLAGS S|D", "EXPRD"
    );
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
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => map_list(&mut service, args::ListCmd).await,
        ModeCmd::Create(cmd) => map_create(&mut service, cmd).await,
        ModeCmd::Delete(cmd) => map_delete(&mut service, cmd).await,
        ModeCmd::Stats(cmd) => map_stats(&mut service, cmd).await,
        ModeCmd::Entries(cmd) => map_entries(&mut service, cmd).await,
        ModeCmd::InsertLayer(cmd) => map_insert_layer(&mut service, cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the fwstate-map objects
/// the map service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn map_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_maps(ListMapsRequest {}).await?.into_inner().maps)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

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
