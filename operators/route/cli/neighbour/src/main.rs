//! CLI for YANET route operator (neighbour-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's NeighbourService
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned neighbour tables.

use core::{
    fmt::{self, Display, Formatter},
    net::IpAddr,
    time::Duration,
};
use std::time::{SystemTime, UNIX_EPOCH};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::{IpAddress, MacAddress};
use netip::MacAddr;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    display::print_table_from_entries,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

use crate::operatorpb::{
    CreateNeighbourTableRequest, ListNeighbourTablesRequest, ListNeighboursRequest,
    NeighbourEntry as ProtoNeighbourEntry, NeighbourTableInfo, RemoveNeighbourTableRequest, RemoveNeighboursRequest,
    UpdateNeighbourTableRequest, UpdateNeighboursRequest, neighbour_service_client::NeighbourServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "operators.route.operatorpb.v1.NeighbourService";

/// Maps a genuine "table not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested table");

/// Neighbour operator CLI (neighbour table management).
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
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show current neighbours.
    Show(ShowCmd),
    /// Add one or more static neighbour entries.
    Add(AddCmd),
    /// Remove one or more neighbour entries.
    Remove(RemoveCmd),
    /// Neighbour table operations.
    Table(TableCmd),
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
    #[arg(long)]
    pub table: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct AddCmd {
    /// Next-hop IP address.
    pub next_hop: String,
    /// MAC address of the next-hop device (neighbour MAC).
    #[arg(long)]
    pub link_addr: String,
    /// MAC address of the local interface.
    #[arg(long)]
    pub hardware_addr: String,
    /// Network interface name.
    #[arg(long)]
    pub device: Option<String>,
    /// Neighbour table name. Defaults to "static".
    #[arg(long)]
    pub table: Option<String>,
    /// Priority for this entry (lower wins). Defaults to the table's
    /// default priority.
    #[arg(long)]
    pub priority: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveCmd {
    /// Next-hop IP address(es) to remove.
    pub next_hops: Vec<String>,
    /// Neighbour table name. Defaults to "static".
    #[arg(long)]
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
    pub name: String,
    /// New default priority for entries in this table.
    #[arg(long)]
    pub default_priority: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveTableCmd {
    /// Table name.
    pub name: String,
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

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = NeighbourService::new(&cmd.connection).await?;

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
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            NeighbourServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn show_neighbours(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ListNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
        };
        let resource = cmd.table.as_ref().map(|table| format!("table '{table}'"));

        let response = self
            .service
            .client()
            .list(request)
            .await
            .map_err(|status| NOT_FOUND.map(status, "show", self.service.endpoint(), resource.as_deref()))?
            .into_inner();

        let empty_message = match &cmd.table {
            Some(table) => format!("no neighbours in {table}"),
            None => "no neighbours".to_owned(),
        };

        output::data(
            &response.neighbours,
            response.neighbours.is_empty(),
            format_args!("{empty_message}"),
            || {
                let mut entries: Vec<NeighbourEntry> =
                    response.neighbours.iter().cloned().map(NeighbourEntry::from).collect();
                entries.sort_by(|a, b| (a.state, &a.next_hop).cmp(&(b.state, &b.next_hop)));
                print_table_from_entries(entries);
            },
        );

        Ok(())
    }

    pub async fn update_neighbour(&mut self, cmd: AddCmd) -> Result<(), Error> {
        let link_addr = parse_mac(&cmd.link_addr).map_err(|err| self.service.invalid("add", err))?;
        let hardware_addr = parse_mac(&cmd.hardware_addr).map_err(|err| self.service.invalid("add", err))?;
        let next_hop = cmd
            .next_hop
            .parse::<IpAddress>()
            .map_err(|err| self.service.invalid("add", err.to_string()))?;
        let table = cmd.table.clone().unwrap_or_else(|| "static".to_owned());

        let request = UpdateNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
            entries: vec![ProtoNeighbourEntry {
                next_hop: Some(next_hop),
                link_addr: Some(link_addr),
                hardware_addr: Some(hardware_addr),
                priority: cmd.priority.unwrap_or_default(),
                device: cmd.device.clone().unwrap_or_default(),
                ..Default::default()
            }],
        };

        self.service
            .client()
            .update_neighbours(request)
            .await
            .map_err(self.service.status("add"))?;

        output::success(
            "add",
            format_args!(
                "Added neighbour {} ({}) to table {}.",
                cmd.next_hop, cmd.link_addr, table
            ),
        );

        Ok(())
    }

    pub async fn remove_neighbours(&mut self, cmd: RemoveCmd) -> Result<(), Error> {
        let next_hops = cmd
            .next_hops
            .iter()
            .map(|next_hop| next_hop.parse::<IpAddress>().map_err(|err| err.to_string()))
            .collect::<Result<Vec<_>, String>>()
            .map_err(|err| self.service.invalid("remove", err))?;
        let table = cmd.table.clone().unwrap_or_else(|| "static".to_owned());

        let request = RemoveNeighboursRequest {
            table: cmd.table.clone().unwrap_or_default(),
            next_hops,
        };

        self.service
            .client()
            .remove_neighbours(request)
            .await
            .map_err(self.service.status("remove"))?;

        output::success(
            "remove",
            format_args!("Removed {} from table {}.", cmd.next_hops.join(", "), table),
        );

        Ok(())
    }

    pub async fn list_tables(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .client()
            .list_tables(ListNeighbourTablesRequest {})
            .await
            .map_err(self.service.status("list tables"))?
            .into_inner();

        output::data(
            &response.tables,
            response.tables.is_empty(),
            format_args!("no neighbour tables"),
            || {
                let entries: Vec<TableEntry> = response.tables.iter().cloned().map(TableEntry::from).collect();
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
            .client()
            .create_table(request)
            .await
            .map_err(self.service.status("create table"))?;

        output::success("create table", format_args!("Created neighbour table {}.", cmd.name));

        Ok(())
    }

    pub async fn update_table(&mut self, cmd: UpdateTableCmd) -> Result<(), Error> {
        let request = UpdateNeighbourTableRequest {
            name: cmd.name.clone(),
            default_priority: cmd.default_priority,
        };

        self.service
            .client()
            .update_table(request)
            .await
            .map_err(self.service.status("update table"))?;

        output::success(
            "update table",
            format_args!(
                "Updated neighbour table {} (default priority {}).",
                cmd.name, cmd.default_priority
            ),
        );

        Ok(())
    }

    pub async fn remove_table(&mut self, cmd: RemoveTableCmd) -> Result<(), Error> {
        let request = RemoveNeighbourTableRequest { name: cmd.name.clone() };

        self.service
            .client()
            .remove_table(request)
            .await
            .map_err(self.service.status("remove table"))?;

        output::success("remove table", format_args!("Removed neighbour table {}.", cmd.name));

        Ok(())
    }
}

fn parse_mac(s: &str) -> Result<MacAddress, String> {
    s.parse::<MacAddr>().map(MacAddr::into).map_err(|err| err.to_string())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct State(pub i32);

impl Display for State {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let v = match self {
            Self(0x00) => "NONE",
            Self(0x01) => "INCOMPLETE",
            Self(0x02) => "REACHABLE",
            Self(0x04) => "STALE",
            Self(0x08) => "DELAY",
            Self(0x10) => "PROBE",
            Self(0x20) => "FAILED",
            Self(0x40) => "NOARP",
            Self(0x80) => "PERMANENT",
            Self(..) => "UNKNOWN",
        };
        write!(f, "{v}")
    }
}

#[derive(Debug)]
pub struct Age(pub SystemTime);

impl Display for Age {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let now = SystemTime::now();
        let duration = match self {
            Self(timestamp) => now.duration_since(*timestamp).unwrap_or_default(),
        };
        write!(f, "{duration:.2?}")
    }
}

#[derive(Debug, Tabled)]
pub struct NeighbourEntry {
    #[tabled(rename = "NEXTHOP")]
    pub next_hop: IpAddr,
    #[tabled(rename = "NEIGHBOUR MAC")]
    pub link_addr: MacAddr,
    #[tabled(rename = "INTERFACE MAC")]
    pub hardware_addr: MacAddr,
    #[tabled(rename = "DEVICE")]
    pub device: String,
    #[tabled(rename = "STATE")]
    pub state: State,
    #[tabled(rename = "AGE")]
    pub age: Age,
    #[tabled(rename = "SOURCE")]
    pub source: String,
    #[tabled(rename = "PRIORITY")]
    pub priority: u32,
}

impl From<ProtoNeighbourEntry> for NeighbourEntry {
    fn from(entry: ProtoNeighbourEntry) -> Self {
        let updated_at = UNIX_EPOCH + Duration::from_secs(entry.updated_at as u64);
        let next_hop = IpAddr::try_from(entry.next_hop.as_ref().expect("neighbour entry missing next_hop"))
            .expect("neighbour entry has invalid next_hop");
        let link_addr = entry
            .link_addr
            .as_ref()
            .map(|addr| MacAddr::try_from(addr).expect("neighbour entry has invalid link_addr"))
            .unwrap_or_else(|| MacAddr::from(0));
        let hardware_addr = entry
            .hardware_addr
            .as_ref()
            .map(|addr| MacAddr::try_from(addr).expect("neighbour entry has invalid hardware_addr"))
            .unwrap_or_else(|| MacAddr::from(0));

        Self {
            next_hop,
            link_addr,
            hardware_addr,
            device: entry.device,
            state: State(entry.state),
            age: Age(updated_at),
            source: entry.source,
            priority: entry.priority,
        }
    }
}

#[derive(Debug, Tabled)]
pub struct TableEntry {
    #[tabled(rename = "NAME")]
    pub name: String,
    #[tabled(rename = "DEFAULT PRIORITY")]
    pub default_priority: u32,
    #[tabled(rename = "ENTRIES")]
    pub entry_count: i64,
    #[tabled(rename = "BUILT-IN")]
    pub built_in: bool,
}

impl From<NeighbourTableInfo> for TableEntry {
    fn from(table: NeighbourTableInfo) -> Self {
        Self {
            name: table.name,
            default_priority: table.default_priority,
            entry_count: table.entry_count,
            built_in: table.built_in,
        }
    }
}
