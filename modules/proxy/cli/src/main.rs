use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum, Subcommand};
use clap_complete::CompleteEnv;
use code::{
    DeleteConfigRequest, ShowConfigRequest, ShowConfigResponse, proxy_service_client::ProxyServiceClient,
    SetAddrRequest,
};
use ptree::TreeBuilder;
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

use crate::code::ListConfigsRequest;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;
    tonic::include_proto!("proxypb");
}

/// Proxy module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    Delete(DeleteCmd),

    Addr {
        #[clap(subcommand)]
        cmd: AddrCmd,
    }
}

#[derive(Debug, Clone, Subcommand)]
pub enum AddrCmd {
    Set(SetAddrCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module config to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct SetAddrCmd {
    #[arg(long = "cfg", short)]
    pub config_name: String,
    #[arg(long)]
    pub addr: u32,
}

pub struct ProxyService {
    client: ProxyServiceClient<Channel>,
}

impl ProxyService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = ProxyServiceClient::connect(endpoint).await?;
        let client = client
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        let mut tree = TreeBuilder::new("List Proxy Configs".to_string());
        for config in response.configs {
            tree.add_empty_child(config);
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self.client.show_config(request).await?.into_inner();
        log::debug!("show config response: {response:?}");

        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.to_owned() };
        self.client.delete_config(request).await?;
        Ok(())
    }

    pub async fn set_addr(&mut self, cmd: SetAddrCmd) -> Result<(), Box<dyn Error>> {
        let request = SetAddrRequest {
            name: cmd.config_name.to_owned(),
            addr: cmd.addr,
        };
        log::debug!("SetAddrRequest: {request:?}");
        let response = self.client.set_addr(request).await?;
        log::debug!("SetAddrResponse: {response:?}");
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ProxyService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Addr { cmd } => match cmd {
            AddrCmd::Set(cmd) => service.set_addr(cmd).await,
        },
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Proxy config".to_string());

    if let Some(config) = &resp.config {
        tree.add_empty_child(format!("Addr: {}", config.addr));
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
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
