use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use commonpb::TargetModule;
use decappb::{
    AddPrefixesRequest, ListConfigsRequest, RemovePrefixesRequest, ShowConfigRequest, ShowConfigResponse,
    decap_service_client::DecapServiceClient,
};
use ipnet::IpNet;
use ptree::TreeBuilder;
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

#[allow(non_snake_case)]
pub mod decappb {
    use serde::Serialize;

    tonic::include_proto!("decappb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// Decap module.
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

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    PrefixAdd(AddPrefixesCmd),
    PrefixRemove(RemovePrefixesCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Decap module name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixesCmd {
    /// Decap module name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Prefix to be added to the input filter of the decapsulation module.
    #[arg(long, short)]
    pub prefix: Vec<IpNet>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixesCmd {
    /// Decap module name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Prefix to be removed from the input filter of the decapsulation module.
    #[arg(long, short)]
    pub prefix: Vec<IpNet>,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
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

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = DecapService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::PrefixAdd(cmd) => service.add_prefixes(cmd).await,
        ModeCmd::PrefixRemove(cmd) => service.remove_prefixes(cmd).await,
    }
}

pub struct DecapService {
    client: DecapServiceClient<Channel>,
}

impl DecapService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = DecapServiceClient::connect(endpoint).await?;
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

        let mut tree = TreeBuilder::new("List Decap Configs".to_string());
        for config in response.configs {
            tree.add_empty_child(config);
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            target: Some(TargetModule {
                config_name: cmd.config_name.to_owned(),
            }),
        };
        log::trace!("show config request: {request:?}");
        let response = self.client.show_config(request).await?.into_inner();
        log::debug!("show config response: {response:?}");

        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn add_prefixes(&mut self, cmd: AddPrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = AddPrefixesRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("AddPrefixesRequest: {request:?}");
        let response = self.client.add_prefixes(request).await?.into_inner();
        log::debug!("AddPrefixesResponse: {response:?}");
        Ok(())
    }

    pub async fn remove_prefixes(&mut self, cmd: RemovePrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = RemovePrefixesRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("RemovePrefixesRequest: {request:?}");
        let response = self.client.remove_prefixes(request).await?.into_inner();
        log::debug!("RemovePrefixesResponse: {response:?}");
        Ok(())
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Decap Prefixes".to_string());

    for (idx, prefix) in resp.prefixes.iter().enumerate() {
        tree.add_empty_child(format!("{idx}: {prefix}"));
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
}
