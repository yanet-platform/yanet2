//! CLI for YANET route operator (neighbour-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's NeighbourService
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned neighbour tables.

use core::net::IpAddr;
use std::{borrow::Cow, path::PathBuf, time::SystemTime};

use clap::{CommandFactory, Parser, ValueEnum};
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
    client::{self, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    humanfmt, output, yaml,
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

/// Largest request this client sends.
///
/// A whole table travels in one message, so the four mebibytes tonic
/// defaults to would bound a legitimate load; this is the ceiling the
/// operator and the gateway accept.
const MAX_REQUEST_SIZE: usize = 256 * 1024 * 1024;

/// Largest reply this client accepts.
///
/// A listing repeats every entry back with the state, the age and the
/// origin the operator stamps on it, so the reply describing a table
/// pushed at the request ceiling outgrows the request that made it. The
/// room is reachable on a direct connection; a gateway in the path bounds
/// the reply at its own ceiling.
const MAX_REPLY_SIZE: usize = 4 * MAX_REQUEST_SIZE;

/// The table a mutation addresses when the caller names none.
///
/// The operator applies the same default, so an unnamed table must resolve
/// to the same one on both sides for a success message to be truthful.
const DEFAULT_TABLE: &str = "static";

/// Resolves the table name a command carries, treating an empty name as no
/// name at all.
///
/// An empty name reaches the operator as no name, so collapsing the two
/// here keeps the table that is reported and the table that changes the
/// same one.
fn named_table(table: Option<&String>) -> Option<&str> {
    table.map(String::as_str).filter(|name| !name.is_empty())
}

fn client(channel: LayeredChannel) -> NeighbourServiceClient<LayeredChannel> {
    NeighbourServiceClient::new(channel)
        .max_decoding_message_size(MAX_REPLY_SIZE)
        .max_encoding_message_size(MAX_REQUEST_SIZE)
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
    /// Add a static neighbour entry.
    ///
    /// The operator records the entry as permanent, so it never ages out
    /// and only an explicit removal drops it. An entry already present
    /// under the same next hop is overwritten.
    Add(AddCmd),
    /// Add the static neighbours listed in a YAML file.
    ///
    /// The file holds a "neighbours" list, naming the same keys the
    /// operator's own seed configuration names: "next_hop", "link_addr"
    /// for the neighbour's own MAC, "hardware_addr" for the MAC of the
    /// local interface, and optionally "device" and "priority". The table
    /// is named by the flag rather than in the file.
    ///
    /// The whole file reaches the operator in a single request, and every
    /// neighbour in it is recorded as permanent, exactly as a single add.
    /// Neighbours the file does not mention are left untouched.
    Update(UpdateCmd),
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
            Self::Update(..) => "update",
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
    /// Show only the entries in this state.
    #[arg(long)]
    pub state: Option<StateFilter>,
    /// Show only the entries reachable over this network interface. An
    /// empty name selects the entries that name no interface.
    #[arg(long, short = 'd')]
    pub device: Option<String>,
}

impl ShowCmd {
    /// Reports whether an entry survives the requested filters.
    ///
    /// A filter left unset admits everything, so an unfiltered command
    /// keeps the whole listing.
    fn matches(&self, entry: &ProtoNeighbourEntry) -> bool {
        let state = self
            .state
            .is_none_or(|state| reported_state(entry.state) == NeighbourState::from(state));
        let device = self.device.as_ref().is_none_or(|device| entry.device == *device);

        state && device
    }

    /// Reports whether the command narrows the listing beyond its table.
    fn filtered(&self) -> bool {
        self.state.is_some() || self.device.is_some()
    }
}

/// The neighbour states a listing can be narrowed to.
///
/// The set mirrors the kernel's neighbour states, which the operator
/// reports unchanged.
#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum StateFilter {
    None,
    Incomplete,
    Reachable,
    Stale,
    Delay,
    Probe,
    Failed,
    Noarp,
    Permanent,
    Unknown,
}

impl From<StateFilter> for NeighbourState {
    fn from(filter: StateFilter) -> Self {
        match filter {
            StateFilter::None => Self::NudNone,
            StateFilter::Incomplete => Self::NudIncomplete,
            StateFilter::Reachable => Self::NudReachable,
            StateFilter::Stale => Self::NudStale,
            StateFilter::Delay => Self::NudDelay,
            StateFilter::Probe => Self::NudProbe,
            StateFilter::Failed => Self::NudFailed,
            StateFilter::Noarp => Self::NudNoarp,
            StateFilter::Permanent => Self::NudPermanent,
            StateFilter::Unknown => Self::NudUnknown,
        }
    }
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
pub struct UpdateCmd {
    /// YAML file listing the neighbours to add.
    pub file: PathBuf,
    /// Neighbour table name. Defaults to "static".
    #[arg(long, add = ArgValueCandidates::new(table_candidates))]
    pub table: Option<String>,
}

/// A file of static neighbours, as the caller writes it.
#[derive(Debug, Default, serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct NeighbourFile {
    #[serde(default)]
    neighbours: Vec<FileNeighbour>,
}

/// One static neighbour, as the caller writes it.
///
/// The state a neighbour is in, when it last changed and where it came
/// from all belong to the operator, so a file cannot set them; refusing
/// an unrecognised key reports such an attempt instead of discarding it
/// in silence.
#[derive(Debug, serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct FileNeighbour {
    next_hop: IpAddress,
    link_addr: MacAddress,
    hardware_addr: MacAddress,
    #[serde(default)]
    priority: u32,
    #[serde(default)]
    device: String,
}

impl From<FileNeighbour> for ProtoNeighbourEntry {
    fn from(neighbour: FileNeighbour) -> Self {
        let FileNeighbour {
            next_hop,
            link_addr,
            hardware_addr,
            priority,
            device,
        } = neighbour;

        Self {
            next_hop: Some(next_hop),
            link_addr: Some(link_addr),
            hardware_addr: Some(hardware_addr),
            priority,
            device,
            ..Default::default()
        }
    }
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

    // The file is read before the connection, so a missing or malformed
    // one fails the same way whether or not the operator can be reached.
    let entries = match &cmd.mode {
        ModeCmd::Update(update) => {
            let endpoint = client::resolve_label(&cmd.globals.connection, action)?;
            let file: NeighbourFile = yaml::load_document(&update.file)
                .map_err(|err| Error::invalid_argument(action, endpoint.clone(), err.to_string()))?;
            if file.neighbours.is_empty() {
                return Err(Error::invalid_argument(
                    action,
                    endpoint,
                    format!("{} names no neighbours", update.file.display()),
                ));
            }

            file.neighbours.into_iter().map(Into::into).collect()
        }
        _ => Vec::new(),
    };

    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::Show(args) => show_neighbours(&mut service, args).await,
        ModeCmd::Add(args) => update_neighbour(&mut service, args).await,
        ModeCmd::Update(args) => update_neighbours(&mut service, args, entries).await,
        ModeCmd::Remove(args) => remove_neighbours(&mut service, args).await,
        ModeCmd::Table(cmd) => match cmd.action {
            TableAction::Show => list_tables(&mut service).await,
            TableAction::Create(args) => create_table(&mut service, args).await,
            TableAction::Update(args) => update_table(&mut service, args).await,
            TableAction::Remove(args) => remove_table(&mut service, args).await,
        },
    }
}

type NeighbourService = Service<NeighbourServiceClient<LayeredChannel>>;

async fn show_neighbours(service: &mut NeighbourService, cmd: ShowCmd) -> Result<(), Error> {
    let table = named_table(cmd.table.as_ref());
    let request = ListNeighboursRequest {
        table: table.unwrap_or_default().to_owned(),
    };
    let resource = table.map(|table| format!("table '{table}'"));

    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", resource.as_deref().unwrap_or("requested table")),
            async |client, request| client.list(request).await,
        )
        .await?;

    let neighbours: Vec<ProtoNeighbourEntry> = response
        .neighbours
        .into_iter()
        .filter(|entry| cmd.matches(entry))
        .collect();

    output::data(
        || &neighbours,
        || {
            if neighbours.is_empty() {
                let scope = match table {
                    Some(table) => format!(" for table '{table}'"),
                    None => String::new(),
                };
                if cmd.filtered() {
                    output::empty(format_args!("No neighbours match the filters{scope}."));
                } else {
                    output::empty(format_args!("No neighbours found{scope}."));
                }
                return;
            }

            let mut entries: Vec<&ProtoNeighbourEntry> = neighbours.iter().collect();
            entries.sort_by_cached_key(|entry| {
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

async fn update_neighbour(service: &mut NeighbourService, cmd: AddCmd) -> Result<(), Error> {
    let table = named_table(cmd.table.as_ref()).unwrap_or(DEFAULT_TABLE);

    let request = UpdateNeighboursRequest {
        table: table.to_owned(),
        entries: vec![ProtoNeighbourEntry {
            next_hop: Some(IpAddress::from(cmd.next_hop)),
            link_addr: Some(MacAddress::from(cmd.link_addr)),
            hardware_addr: Some(MacAddress::from(cmd.hardware_addr)),
            priority: cmd.priority.unwrap_or_default(),
            device: cmd.device.clone().unwrap_or_default(),
            ..Default::default()
        }],
    };

    service
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

async fn update_neighbours(
    service: &mut NeighbourService,
    cmd: UpdateCmd,
    entries: Vec<ProtoNeighbourEntry>,
) -> Result<(), Error> {
    let table = named_table(cmd.table.as_ref()).unwrap_or(DEFAULT_TABLE);
    let count = entries.len();

    let request = UpdateNeighboursRequest { table: table.to_owned(), entries };

    service
        .unary("update", request, async |client, request| {
            client.update_neighbours(request).await
        })
        .await?;

    let noun = if count == 1 { "entry" } else { "entries" };
    output::success("update", format_args!("Applied {count} {noun} to table '{table}'."));

    Ok(())
}

async fn remove_neighbours(service: &mut NeighbourService, cmd: RemoveCmd) -> Result<(), Error> {
    let table = named_table(cmd.table.as_ref()).unwrap_or(DEFAULT_TABLE);

    let request = RemoveNeighboursRequest {
        table: table.to_owned(),
        next_hops: cmd.next_hops.iter().copied().map(IpAddress::from).collect(),
    };

    service
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

async fn list_tables(service: &mut NeighbourService) -> Result<(), Error> {
    let response = service
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

async fn create_table(service: &mut NeighbourService, cmd: CreateTableCmd) -> Result<(), Error> {
    let request = CreateNeighbourTableRequest {
        name: cmd.name.clone(),
        default_priority: cmd.default_priority,
    };

    service
        .unary("create table", request, async |client, request| {
            client.create_table(request).await
        })
        .await?;

    output::success("create table", format_args!("Created table '{}'.", cmd.name));

    Ok(())
}

async fn update_table(service: &mut NeighbourService, cmd: UpdateTableCmd) -> Result<(), Error> {
    let request = UpdateNeighbourTableRequest {
        name: cmd.name.clone(),
        default_priority: cmd.default_priority,
    };

    service
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

async fn remove_table(service: &mut NeighbourService, cmd: RemoveTableCmd) -> Result<(), Error> {
    let request = RemoveNeighbourTableRequest { name: cmd.name.clone() };

    service
        .unary_with(
            "remove table",
            request,
            service.not_found("remove table", &format!("table '{}'", cmd.name)),
            async |client, request| client.remove_table(request).await,
        )
        .await?;

    output::success("remove table", format_args!("Removed table '{}'.", cmd.name));

    Ok(())
}

/// Resolves a reported state, standing in the unknown one for a value the
/// wire contract does not define.
///
/// Every reader of a reported state goes through here, so a listing and a
/// filter agree on what an undefined value is.
fn reported_state(value: i32) -> NeighbourState {
    NeighbourState::try_from(value).unwrap_or(NeighbourState::NudUnknown)
}

/// Returns the proto-defined name for a `NeighbourState` discriminant,
/// stripped of its `NUD_` prefix (e.g. `"REACHABLE"`).
fn state_name(value: i32) -> &'static str {
    serde_with::short_name(reported_state(value).as_str_name(), "NUD_")
}

/// Serializes the `state` field of `NeighbourEntry` as its proto-defined
/// name (e.g. `"REACHABLE"`) instead of the raw `i32` enum discriminant.
pub fn serialize_neighbour_state<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(state_name(*value))
}

/// Renders how long ago an entry last changed.
///
/// A dash stands in for an entry the operator has never stamped. Such an
/// entry carries the far side's zero instant, which lies well before the
/// epoch rather than on it.
fn age(updated_at: i64) -> String {
    if updated_at <= 0 {
        return "-".to_owned();
    }

    humanfmt::age_since(updated_at, SystemTime::now())
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

    /// Builds an entry carrying only the fields a listing filter reads.
    fn entry(state: NeighbourState, device: &str) -> ProtoNeighbourEntry {
        ProtoNeighbourEntry {
            state: state as i32,
            device: device.to_owned(),
            ..Default::default()
        }
    }

    /// Builds a listing command over the merged view with the given
    /// filters.
    fn show(state: Option<StateFilter>, device: Option<&str>) -> ShowCmd {
        ShowCmd {
            table: None,
            state,
            device: device.map(ToOwned::to_owned),
        }
    }

    #[test]
    fn test_show_filters_unset_keep_every_entry() {
        let cmd = show(None, None);

        assert!(cmd.matches(&entry(NeighbourState::NudStale, "eth0")));
        assert!(!cmd.filtered());
    }

    #[test]
    fn test_show_filters_state_drops_every_other_state() {
        let cmd = show(Some(StateFilter::Reachable), None);

        assert!(cmd.matches(&entry(NeighbourState::NudReachable, "eth0")));
        assert!(!cmd.matches(&entry(NeighbourState::NudStale, "eth0")));
    }

    #[test]
    fn test_show_filters_device_drops_every_other_device() {
        let cmd = show(None, Some("eth0"));

        assert!(cmd.matches(&entry(NeighbourState::NudStale, "eth0")));
        assert!(!cmd.matches(&entry(NeighbourState::NudStale, "eth1")));
    }

    #[test]
    fn test_show_filters_state_and_device_both_have_to_hold() {
        let cmd = show(Some(StateFilter::Permanent), Some("eth0"));

        assert!(cmd.matches(&entry(NeighbourState::NudPermanent, "eth0")));
        assert!(!cmd.matches(&entry(NeighbourState::NudPermanent, "eth1")));
        assert!(!cmd.matches(&entry(NeighbourState::NudStale, "eth0")));
    }

    #[test]
    fn test_show_filters_device_drops_an_entry_with_no_device() {
        let cmd = show(None, Some("eth0"));

        assert!(!cmd.matches(&entry(NeighbourState::NudStale, "")));
    }

    /// Verifies that an entry the listing shows as unknown is selected by
    /// the filter of that name, not dropped for carrying an undefined
    /// discriminant.
    #[test]
    fn test_show_filters_state_unknown_keeps_an_undefined_state() {
        let cmd = show(Some(StateFilter::Unknown), None);
        let undefined = ProtoNeighbourEntry { state: 3, ..Default::default() };

        assert_eq!("UNKNOWN", state_name(undefined.state));
        assert!(cmd.matches(&undefined));
    }

    #[test]
    fn test_named_table_reads_an_empty_name_as_no_name() {
        assert_eq!(None, named_table(Some(&String::new())));
        assert_eq!(None, named_table(None));
    }

    #[test]
    fn test_named_table_keeps_a_spelled_name() {
        assert_eq!(Some("custom"), named_table(Some(&"custom".to_owned())));
    }

    /// Verifies that a state a listing can report always has a filter
    /// spelling that selects it.
    #[test]
    fn test_state_filter_covers_every_reported_state() {
        for value in 0..=i32::from(u8::MAX) {
            let Ok(state) = NeighbourState::try_from(value) else {
                continue;
            };

            assert!(
                StateFilter::value_variants()
                    .iter()
                    .any(|filter| NeighbourState::from(*filter) == state),
                "{state:?} has no filter spelling"
            );
        }
    }

    /// Verifies that a filter is spelled exactly as the state column
    /// renders the state it selects, in lower case.
    #[test]
    fn test_state_filter_spelling_matches_the_rendered_state() {
        for filter in StateFilter::value_variants() {
            let rendered = state_name(NeighbourState::from(*filter) as i32).to_lowercase();
            let spelling = filter.to_possible_value().expect("every filter is selectable");

            assert_eq!(rendered, spelling.get_name());
        }
    }

    #[test]
    fn test_neighbour_file_maps_every_entry_onto_the_wire() {
        let document = "
neighbours:
  - next_hop: 192.0.2.1
    link_addr: aa:bb:cc:dd:ee:ff
    hardware_addr: 11:22:33:44:55:66
    priority: 7
    device: eth0
  - next_hop: 2001:db8::1
    link_addr: aa:bb:cc:dd:ee:01
    hardware_addr: 11:22:33:44:55:02
";

        let file: NeighbourFile = serde_yaml::from_str(document).unwrap();
        let entries: Vec<ProtoNeighbourEntry> = file.neighbours.into_iter().map(Into::into).collect();

        assert_eq!(
            vec![
                ProtoNeighbourEntry {
                    next_hop: Some("192.0.2.1".parse::<IpAddress>().unwrap()),
                    link_addr: Some("aa:bb:cc:dd:ee:ff".parse::<MacAddress>().unwrap()),
                    hardware_addr: Some("11:22:33:44:55:66".parse::<MacAddress>().unwrap()),
                    priority: 7,
                    device: "eth0".to_owned(),
                    ..Default::default()
                },
                ProtoNeighbourEntry {
                    next_hop: Some("2001:db8::1".parse::<IpAddress>().unwrap()),
                    link_addr: Some("aa:bb:cc:dd:ee:01".parse::<MacAddress>().unwrap()),
                    hardware_addr: Some("11:22:33:44:55:02".parse::<MacAddress>().unwrap()),
                    ..Default::default()
                },
            ],
            entries
        );
    }

    #[test]
    fn test_neighbour_file_without_entries_parses_as_empty() {
        let file: NeighbourFile = serde_yaml::from_str("{}").unwrap();

        assert!(file.neighbours.is_empty());
    }

    /// Verifies that a neighbour factored out of an earlier one carries
    /// the keys it inherits, so a repetitive file can be written once.
    #[test]
    fn test_neighbour_file_expands_a_merge_key() {
        let document = "
neighbours:
  - &first
    next_hop: 192.0.2.1
    link_addr: aa:bb:cc:dd:ee:ff
    hardware_addr: 11:22:33:44:55:66
    device: eth0
  - <<: *first
    next_hop: 192.0.2.2
";

        let mut value: serde_yaml::Value = serde_yaml::from_str(document).unwrap();
        value.apply_merge().unwrap();
        let file: NeighbourFile = serde_yaml::from_value(value).unwrap();

        assert_eq!("eth0", file.neighbours[1].device);
        assert_eq!("192.0.2.2", file.neighbours[1].next_hop.to_string());
    }

    #[test]
    fn test_neighbour_file_rejects_an_operator_owned_key() {
        let document = "
neighbours:
  - next_hop: 192.0.2.1
    link_addr: aa:bb:cc:dd:ee:ff
    hardware_addr: 11:22:33:44:55:66
    state: REACHABLE
";

        assert!(serde_yaml::from_str::<NeighbourFile>(document).is_err());
    }

    #[test]
    fn test_neighbour_file_rejects_a_missing_hardware_address() {
        let document = "
neighbours:
  - next_hop: 192.0.2.1
    link_addr: aa:bb:cc:dd:ee:ff
";

        assert!(serde_yaml::from_str::<NeighbourFile>(document).is_err());
    }

    #[test]
    fn test_neighbour_file_rejects_a_malformed_address() {
        let document = "
neighbours:
  - next_hop: 192.0.2.300
    link_addr: aa:bb:cc:dd:ee:ff
    hardware_addr: 11:22:33:44:55:66
";

        assert!(serde_yaml::from_str::<NeighbourFile>(document).is_err());
    }

    /// Verifies that both spellings of "never stamped" render as a dash:
    /// the epoch itself, and the zero instant the operator's own clock
    /// encodes, which lies far before it.
    #[test]
    fn test_age_of_an_unstamped_entry_is_a_dash() {
        const ZERO_INSTANT: i64 = -62_135_596_800;

        assert_eq!("-", age(0));
        assert_eq!("-", age(ZERO_INSTANT));
    }
}
