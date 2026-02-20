//! CLI for YANET "neighbour" module.

use core::{error::Error, time::Duration};
use std::time::UNIX_EPOCH;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    neighbour_client::NeighbourClient, CreateNeighbourTableRequest, ListNeighbourTablesRequest, ListNeighboursRequest,
    MacAddress, NeighbourEntry as ProtoNeighbourEntry, RemoveNeighbourTableRequest, RemoveNeighboursRequest,
    UpdateNeighbourTableRequest, UpdateNeighboursRequest,
};
use netip::MacAddr;
use tabled::{
    settings::{
        object::Columns,
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table,
};
use tonic::codec::CompressionEncoding;
use yanet_cli_neighbour::{Age, NeighbourEntry, State, TableEntry};
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("routepb");
}

/// Neighbour module.
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
    /// Show current neighbors.
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
    /// Neighbour table name.
    ///
    /// Defaults to "static".
    #[arg(long)]
    pub table: Option<String>,
    /// Priority for this entry.
    ///
    /// Lower value means higher priority.
    /// Defaults to the table's default priority.
    #[arg(long)]
    pub priority: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveCmd {
    /// Next-hop IP address(es) to remove.
    pub next_hops: Vec<String>,
    /// Neighbour table name.
    ///
    /// Defaults to "static".
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

/// Service for interacting with the neighbour module of the YANET router.
///
/// Provides methods to retrieve and display neighbor information through
/// gRPC communication with the control plane.
pub struct NeighbourService {
    client: NeighbourClient<LayeredChannel>,
}

impl NeighbourService {
    /// Creates a new NeighbourService connected to the specified endpoint.
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = NeighbourClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    /// Retrieves and displays the current neighbor table in a formatted
    /// table.
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

        let mut table = Table::new(entries);
        table.with(
            Style::modern()
                .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
                .remove_frame()
                .remove_horizontal(),
        );
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));

        println!("{table}");

        Ok(())
    }

    /// Adds or updates a neighbour entry.
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

    /// Removes one or more neighbour entries.
    pub async fn remove_neighbours(&mut self, args: RemoveCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveNeighboursRequest {
            table: args.table.unwrap_or_default(),
            next_hops: args.next_hops,
        };

        self.client.remove_neighbours(request).await?;
        println!("OK");
        Ok(())
    }

    /// Lists all neighbour tables.
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

        let mut table = Table::new(entries);
        table.with(
            Style::modern()
                .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
                .remove_frame()
                .remove_horizontal(),
        );
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));

        println!("{table}");
        Ok(())
    }

    /// Creates a new neighbour table.
    pub async fn create_table(&mut self, args: CreateTableCmd) -> Result<(), Box<dyn Error>> {
        let request = CreateNeighbourTableRequest {
            name: args.name,
            default_priority: args.default_priority,
        };

        self.client.create_table(request).await?;
        println!("OK");
        Ok(())
    }

    /// Updates an existing neighbour table.
    pub async fn update_table(&mut self, args: UpdateTableCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateNeighbourTableRequest {
            name: args.name,
            default_priority: args.default_priority,
        };

        self.client.update_table(request).await?;
        println!("OK");
        Ok(())
    }

    /// Removes a neighbour table.
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
