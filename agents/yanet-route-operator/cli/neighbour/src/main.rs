//! CLI for YANET route operator (neighbour-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's NeighbourService
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned neighbour tables.

use core::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::IpAddr,
    time::Duration,
};
use std::time::{SystemTime, UNIX_EPOCH};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::MacAddress;
use netip::MacAddr;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    display::print_table,
    logging,
};

use crate::operatorpb::{
    CreateNeighbourTableRequest, ListNeighbourTablesRequest, ListNeighboursRequest,
    NeighbourEntry as ProtoNeighbourEntry, RemoveNeighbourTableRequest, RemoveNeighboursRequest,
    UpdateNeighbourTableRequest, UpdateNeighboursRequest, neighbour_service_client::NeighbourServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operatorpb");
}

/// Neighbour operator CLI (neighbour table management).
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
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
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = NeighbourService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Show(args) => service.show_neighbours(args.table).await,
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
    client: NeighbourServiceClient<LayeredChannel>,
}

impl NeighbourService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = NeighbourServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn show_neighbours(&mut self, table: Option<String>) -> Result<(), Box<dyn Error>> {
        let request = ListNeighboursRequest { table: table.unwrap_or_default() };
        let response = self.client.list(request).await?.into_inner();

        let mut entries = response
            .neighbours
            .into_iter()
            .map(|entry| {
                let updated_at = UNIX_EPOCH + Duration::from_secs(entry.updated_at as u64);
                let next_hop = entry.next_hop.parse().unwrap();

                let link_addr = {
                    let addr = entry.link_addr.map(|v| v.addr).unwrap_or_default();
                    MacAddr::from(addr)
                };
                let hardware_addr = {
                    let addr = entry.hardware_addr.map(|v| v.addr).unwrap_or_default();
                    MacAddr::from(addr)
                };

                NeighbourEntry {
                    next_hop,
                    link_addr,
                    hardware_addr,
                    device: entry.device,
                    state: State(entry.state),
                    age: Age(updated_at),
                    source: entry.source,
                    priority: entry.priority,
                }
            })
            .collect::<Vec<_>>();

        entries.sort_by(|a, b| (a.state, &a.next_hop).cmp(&(b.state, &b.next_hop)));

        print_table(entries);
        Ok(())
    }

    pub async fn update_neighbour(&mut self, args: AddCmd) -> Result<(), Box<dyn Error>> {
        let link_addr = parse_mac(&args.link_addr)?;
        let hardware_addr = parse_mac(&args.hardware_addr)?;

        let request = UpdateNeighboursRequest {
            table: args.table.unwrap_or_default(),
            entries: vec![ProtoNeighbourEntry {
                next_hop: args.next_hop,
                link_addr: Some(link_addr),
                hardware_addr: Some(hardware_addr),
                device: args.device.unwrap_or_default(),
                priority: args.priority.unwrap_or_default(),
                ..Default::default()
            }],
        };

        self.client.update_neighbours(request).await?;
        println!("OK");
        Ok(())
    }

    pub async fn remove_neighbours(&mut self, args: RemoveCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveNeighboursRequest {
            table: args.table.unwrap_or_default(),
            next_hops: args.next_hops,
        };

        self.client.remove_neighbours(request).await?;
        println!("OK");
        Ok(())
    }

    pub async fn list_tables(&mut self) -> Result<(), Box<dyn Error>> {
        let response = self
            .client
            .list_tables(ListNeighbourTablesRequest {})
            .await?
            .into_inner();

        let entries: Vec<TableEntry> = response
            .tables
            .into_iter()
            .map(|t| TableEntry {
                name: t.name,
                default_priority: t.default_priority,
                entry_count: t.entry_count,
                built_in: t.built_in,
            })
            .collect();

        print_table(entries);
        Ok(())
    }

    pub async fn create_table(&mut self, args: CreateTableCmd) -> Result<(), Box<dyn Error>> {
        let request = CreateNeighbourTableRequest {
            name: args.name,
            default_priority: args.default_priority,
        };
        self.client.create_table(request).await?;
        println!("OK");
        Ok(())
    }

    pub async fn update_table(&mut self, args: UpdateTableCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateNeighbourTableRequest {
            name: args.name,
            default_priority: args.default_priority,
        };
        self.client.update_table(request).await?;
        println!("OK");
        Ok(())
    }

    pub async fn remove_table(&mut self, args: RemoveTableCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveNeighbourTableRequest { name: args.name };
        self.client.remove_table(request).await?;
        println!("OK");
        Ok(())
    }
}

fn parse_mac(s: &str) -> Result<MacAddress, Box<dyn Error>> {
    let mac: MacAddr = s.parse()?;
    Ok(MacAddress { addr: mac.as_u64() })
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
