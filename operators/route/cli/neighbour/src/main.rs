//! CLI for YANET route operator (neighbour-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's NeighbourService
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned neighbour tables.

use core::{net::IpAddr, time::Duration};
use std::{
    borrow::Cow,
    time::{SystemTime, UNIX_EPOCH},
};

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::{
    pb::{IpAddress, MacAddress},
    serde_with,
};
use netip::MacAddr;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    output,
};

use crate::operatorpb::{
    CreateNeighbourTableRequest, ListNeighbourTablesRequest, ListNeighboursRequest,
    NeighbourEntry as ProtoNeighbourEntry, NeighbourState, NeighbourTableInfo, RemoveNeighbourTableRequest,
    RemoveNeighboursRequest, UpdateNeighbourTableRequest, UpdateNeighboursRequest,
    neighbour_service_client::NeighbourServiceClient,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "operators.route.operatorpb.v1.NeighbourService";

fn client(channel: LayeredChannel) -> NeighbourServiceClient<LayeredChannel> {
    NeighbourServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages the neighbour tables of the route operator.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show current neighbours.
    Show(ShowCmd),
    /// Add one or more static neighbour entries.
    Add(AddCmd),
    /// Remove one or more neighbour entries.
    Remove(RemoveCmd),
    /// Manage neighbour tables.
    Table(TableCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Show(..) => "show",
            Self::Add(..) => "add",
            Self::Remove(..) => "remove",
            Self::Table(cmd) => match &cmd.action {
                TableAction::Show => "list tables",
                TableAction::Create(..) => "create table",
                TableAction::Update(..) => "update table",
                TableAction::Remove(..) => "remove table",
            },
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct TableCmd {
    #[clap(subcommand)]
    pub action: TableAction,
}

#[derive(Debug, Clone, Parser)]
pub enum TableAction {
    /// List neighbour tables.
    Show,
    /// Create a new neighbour table.
    Create(CreateTableCmd),
    /// Update an existing neighbour table.
    Update(UpdateTableCmd),
    /// Remove a neighbour table.
    Remove(RemoveTableCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Show entries from a specific table only. If omitted, shows the
    /// merged view.
    #[arg(long, add = ArgValueCandidates::new(table_candidates))]
    pub table: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct AddCmd {
    /// Next-hop IP address.
    pub next_hop: IpAddr,
    /// MAC address of the next-hop device (neighbour MAC).
    #[arg(long)]
    pub link_addr: MacAddr,
    /// MAC address of the local interface.
    #[arg(long)]
    pub hardware_addr: MacAddr,
    /// Network interface name.
    #[arg(long, short = 'd')]
    pub device: Option<String>,
    /// Neighbour table name. Defaults to "static".
    #[arg(long, add = ArgValueCandidates::new(table_candidates))]
    pub table: Option<String>,
    /// Priority for this entry (lower wins). Defaults to the table's
    /// default priority.
    #[arg(long)]
    pub priority: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveCmd {
    /// Next-hop IP address(es) to remove.
    #[arg(required = true)]
    pub next_hops: Vec<IpAddr>,
    /// Neighbour table name. Defaults to "static".
    #[arg(long, add = ArgValueCandidates::new(table_candidates))]
    pub table: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct CreateTableCmd {
    /// Neighbour table name.
    pub name: String,
    /// Default priority for entries in this table.
    #[arg(long)]
    pub default_priority: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateTableCmd {
    /// Neighbour table name.
    #[arg(add = ArgValueCandidates::new(table_candidates))]
    pub name: String,
    /// New default priority for entries in this table.
    #[arg(long)]
    pub default_priority: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveTableCmd {
    /// Table name.
    #[arg(add = ArgValueCandidates::new(table_candidates))]
    pub name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = NeighbourService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::Show(args) => service.show_neighbours(args).await,
        ModeCmd::Add(args) => service.update_neighbour(args).await,
        ModeCmd::Remove(args) => service.remove_neighbours(args).await,
        ModeCmd::Table(cmd) => match cmd.action {
            TableAction::Show => service.list_tables().await,
            TableAction::Create(args) => service.create_table(args).await,
            TableAction::Update(args) => service.update_table(args).await,
            TableAction::Remove(args) => service.remove_table(args).await,
        },
    }
}

pub struct NeighbourService {
    service: Service<NeighbourServiceClient<LayeredChannel>>,
}

impl NeighbourService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, client).await?;

        Ok(Self { service })
    }

    pub async fn show_neighbours(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ListNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
        };
        let resource = cmd.table.as_ref().map(|table| format!("table '{table}'"));

        let response = self
            .service
            .unary_with(
                "show",
                request,
                self.service
                    .not_found("show", resource.as_deref().unwrap_or("requested table")),
                async |client, request| client.list(request).await,
            )
            .await?;

        output::data(
            || &response.neighbours,
            || {
                if response.neighbours.is_empty() {
                    match &cmd.table {
                        Some(table) => output::empty(format_args!("No neighbours found for table '{table}'.")),
                        None => output::empty(format_args!("No neighbours found.")),
                    }
                    return;
                }

                let mut entries: Vec<&ProtoNeighbourEntry> = response.neighbours.iter().collect();
                entries.sort_by_key(|entry| {
                    let next_hop = entry
                        .next_hop
                        .as_ref()
                        .and_then(|addr| IpAddr::try_from(addr).ok())
                        .map(|addr| addr.to_canonical());
                    (entry.state, next_hop)
                });
                print_table_from_entries(entries);
            },
        );

        Ok(())
    }

    pub async fn update_neighbour(&mut self, cmd: AddCmd) -> Result<(), Error> {
        let table = cmd.table.clone().unwrap_or_else(|| "static".to_owned());

        let request = UpdateNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
            entries: vec![ProtoNeighbourEntry {
                next_hop: Some(IpAddress::from(cmd.next_hop)),
                link_addr: Some(MacAddress::from(cmd.link_addr)),
                hardware_addr: Some(MacAddress::from(cmd.hardware_addr)),
                priority: cmd.priority.unwrap_or_default(),
                device: cmd.device.clone().unwrap_or_default(),
                ..Default::default()
            }],
        };

        self.service
            .unary("add", request, async |client, request| {
                client.update_neighbours(request).await
            })
            .await?;

        output::success(
            "add",
            format_args!(
                "Added neighbour {} ({}) to table '{}'.",
                cmd.next_hop, cmd.link_addr, table
            ),
        );

        Ok(())
    }

    pub async fn remove_neighbours(&mut self, cmd: RemoveCmd) -> Result<(), Error> {
        let table = cmd.table.clone().unwrap_or_else(|| "static".to_owned());

        let request = RemoveNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
            next_hops: cmd.next_hops.iter().copied().map(IpAddress::from).collect(),
        };

        self.service
            .unary("remove", request, async |client, request| {
                client.remove_neighbours(request).await
            })
            .await?;

        let next_hops = cmd
            .next_hops
            .iter()
            .map(ToString::to_string)
            .collect::<Vec<_>>()
            .join(", ");
        output::success("remove", format_args!("Removed {next_hops} from table '{table}'."));

        Ok(())
    }

    pub async fn list_tables(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary("list tables", ListNeighbourTablesRequest {}, async |client, request| {
                client.list_tables(request).await
            })
            .await?;

        output::data(
            || &response.tables,
            || {
                if response.tables.is_empty() {
                    output::empty_with_hint(
                        format_args!("No neighbour tables found."),
                        format_args!(
                            "create one with 'yanet-cli-operator-neighbour table create <name> --default-priority <n>'"
                        ),
                    );
                    return;
                }

                let entries: Vec<&NeighbourTableInfo> = response.tables.iter().collect();
                print_table_from_entries(entries);
            },
        );

        Ok(())
    }

    pub async fn create_table(&mut self, cmd: CreateTableCmd) -> Result<(), Error> {
        let request = CreateNeighbourTableRequest {
            name: cmd.name.clone(),
            default_priority: cmd.default_priority,
        };

        self.service
            .unary("create table", request, async |client, request| {
                client.create_table(request).await
            })
            .await?;

        output::success("create table", format_args!("Created table '{}'.", cmd.name));

        Ok(())
    }

    pub async fn update_table(&mut self, cmd: UpdateTableCmd) -> Result<(), Error> {
        let request = UpdateNeighbourTableRequest {
            name: cmd.name.clone(),
            default_priority: cmd.default_priority,
        };

        self.service
            .unary("update table", request, async |client, request| {
                client.update_table(request).await
            })
            .await?;

        output::success(
            "update table",
            format_args!(
                "Updated table '{}' (default priority {}).",
                cmd.name, cmd.default_priority
            ),
        );

        Ok(())
    }

    pub async fn remove_table(&mut self, cmd: RemoveTableCmd) -> Result<(), Error> {
        let request = RemoveNeighbourTableRequest { name: cmd.name.clone() };

        self.service
            .unary_with(
                "remove table",
                request,
                self.service.not_found("remove table", &format!("table '{}'", cmd.name)),
                async |client, request| client.remove_table(request).await,
            )
            .await?;

        output::success("remove table", format_args!("Removed table '{}'.", cmd.name));

        Ok(())
    }
}

/// Returns the proto-defined name for a `NeighbourState` discriminant,
/// stripped of its `NUD_` prefix (e.g. `"REACHABLE"`).
///
/// An unrecognized discriminant falls back to `NeighbourState::NudUnknown`.
fn state_name(value: i32) -> &'static str {
    let state = NeighbourState::try_from(value).unwrap_or(NeighbourState::NudUnknown);

    serde_with::short_name(state.as_str_name(), "NUD_")
}

/// Serializes the `state` field of `NeighbourEntry` as its proto-defined
/// name (e.g. `"REACHABLE"`) instead of the raw `i32` enum discriminant.
pub fn serialize_neighbour_state<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(state_name(*value))
}

/// Formats the time elapsed since a neighbour entry's `updated_at` Unix
/// timestamp, clamping a negative timestamp to the epoch to avoid an
/// overflow panic.
fn age(updated_at: i64) -> String {
    let updated_at = UNIX_EPOCH + Duration::from_secs(updated_at.max(0) as u64);
    let elapsed = SystemTime::now().duration_since(updated_at).unwrap_or_default();
    format!("{elapsed:.2?}")
}

impl Tabled for ProtoNeighbourEntry {
    const LENGTH: usize = 8;

    fn fields(&self) -> Vec<Cow<'_, str>> {
        vec![
            Cow::Owned(self.next_hop.clone().unwrap_or_default().to_string()),
            Cow::Owned(self.link_addr.unwrap_or_default().to_string()),
            Cow::Owned(self.hardware_addr.unwrap_or_default().to_string()),
            Cow::Borrowed(self.device.as_str()),
            Cow::Borrowed(state_name(self.state)),
            Cow::Owned(age(self.updated_at)),
            Cow::Borrowed(self.source.as_str()),
            Cow::Owned(self.priority.to_string()),
        ]
    }

    fn headers() -> Vec<Cow<'static, str>> {
        vec![
            Cow::Borrowed("NEXTHOP"),
            Cow::Borrowed("NEIGHBOUR MAC"),
            Cow::Borrowed("INTERFACE MAC"),
            Cow::Borrowed("DEVICE"),
            Cow::Borrowed("STATE"),
            Cow::Borrowed("AGE"),
            Cow::Borrowed("SOURCE"),
            Cow::Borrowed("PRIORITY"),
        ]
    }
}

impl Tabled for NeighbourTableInfo {
    const LENGTH: usize = 4;

    fn fields(&self) -> Vec<Cow<'_, str>> {
        vec![
            Cow::Borrowed(self.name.as_str()),
            Cow::Owned(self.default_priority.to_string()),
            Cow::Owned(self.entry_count.to_string()),
            Cow::Owned(self.built_in.to_string()),
        ]
    }

    fn headers() -> Vec<Cow<'static, str>> {
        vec![
            Cow::Borrowed("NAME"),
            Cow::Borrowed("DEFAULT PRIORITY"),
            Cow::Borrowed("ENTRIES"),
            Cow::Borrowed("BUILT-IN"),
        ]
    }
}

/// Completion candidates for a table-name argument: the neighbour tables
/// the operator currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn table_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client
            .list_tables(ListNeighbourTablesRequest {})
            .await?
            .into_inner()
            .tables
            .into_iter()
            .map(|table| table.name)
            .collect())
    })
}

#[cfg(test)]
mod test {
    use super::*;

    /// Pins the JSON shape of a serialized `NeighbourEntry`.
    ///
    /// `next_hop`/`link_addr` need no `serialize_with` override, since
    /// commonpb's own `Serialize` impl already renders them as plain address
    /// strings. `state` does need one, via `serialize_neighbour_state`.
    #[test]
    fn neighbour_entry_serializes_addresses_as_strings_and_state_as_its_name() {
        let entry = ProtoNeighbourEntry {
            next_hop: Some(IpAddress::from(IpAddr::V4(core::net::Ipv4Addr::new(192, 0, 2, 1)))),
            link_addr: Some("aa:bb:cc:dd:ee:ff".parse::<MacAddress>().unwrap()),
            state: NeighbourState::NudReachable as i32,
            ..Default::default()
        };

        let json = serde_json::to_string(&entry).unwrap();

        assert_eq!(
            r#"{"next_hop":"192.0.2.1","link_addr":"aa:bb:cc:dd:ee:ff","hardware_addr":null,"state":"REACHABLE","updated_at":0,"source":"","priority":0,"device":""}"#,
            json
        );
    }
}
