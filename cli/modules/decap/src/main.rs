use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use ipnet::IpNet;

use code::{
    decap_service_client::DecapServiceClient, AddPrefixesRequest, RemovePrefixesRequest, ShowConfigRequest,
    ShowConfigResponse, TargetModule,
};
use ptree::TreeBuilder;
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("decappb");
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
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Show(ShowConfigCmd),
    AddPrefixes(AddPrefixesCmd),
    RemovePrefixes(RemovePrefixesCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Decap module name to operate on.
    #[arg(long = "mod")]
    pub module_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixesCmd {
    /// Decap module name to operate on.
    #[arg(long = "mod")]
    pub module_name: String,
    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// Prefix to be added to the input filter of the decapsulation module.
    #[arg(long)]
    pub prefix: Vec<IpNet>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixesCmd {
    /// Decap module name to operate on.
    #[arg(long = "mod")]
    pub module_name: String,

    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// Prefix to be removed from the input filter of the decapsulation module.
    #[arg(long)]
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

    let format = cmd.format;
    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd, format).await,
        ModeCmd::AddPrefixes(cmd) => service.add_prefixes(cmd).await,
        ModeCmd::RemovePrefixes(cmd) => service.remove_prefixes(cmd).await,
    }
}

pub struct DecapService {
    client: DecapServiceClient<Channel>,
}

impl DecapService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = DecapServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd, format: OutputFormat) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name.to_owned(),
                numa: Vec::new(),
            }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        match format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn add_prefixes(&mut self, cmd: AddPrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = AddPrefixesRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::debug!("AddPrefixesRequest: {:?}", request);
        let response = self.client.add_prefixes(request).await?.into_inner();
        log::debug!("AddPrefixesResponse: {:?}", response);
        Ok(())
    }

    pub async fn remove_prefixes(&mut self, cmd: RemovePrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = RemovePrefixesRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::debug!("RemovePrefixesRequest: {:?}", request);
        let response = self.client.remove_prefixes(request).await?.into_inner();
        log::debug!("RemovePrefixesResponse: {:?}", response);
        Ok(())
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Decap Configs".to_string());

    for config in &resp.configs {
        tree.begin_child(format!("NUMA {}", config.numa));

        tree.begin_child("Prefixes".to_string());
        for (idx, prefix) in config.prefixes.iter().enumerate() {
            tree.add_empty_child(format!("{}: {}", idx, prefix));
        }
        tree.end_child();

        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
}
