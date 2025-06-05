use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use ipnet::IpNet;
use ptree::TreeBuilder;
use tonic::transport::Channel;

use code::{
    AddPrefixesRequest, DscpConfig, RemovePrefixesRequest, SetDscpMarkingRequest,
    ShowConfigRequest, ShowConfigResponse, TargetModule, dscp_service_client::DscpServiceClient,
};
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("dscppb");
}

/// DSCP module for packet marking.
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
    Show(ShowConfigCmd),
    PrefixAdd(AddPrefixesCmd),
    PrefixRemove(RemovePrefixesCmd),
    SetMarking(SetDscpMarkingCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// DSCP module name to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Dataplane instances where the changes should be applied, optional.
    ///
    /// If no instances specified, the route will be applied to all instances.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,

    /// Prefix to be added to the input filter of the DSCP module.
    #[arg(long, short)]
    pub prefix: Vec<IpNet>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,

    /// Dataplane instances where the changes should be applied, optional.
    ///
    /// If no instances specified, the route will be applied to all instances.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,

    /// Prefix to be removed from the input filter of the DSCP module.
    #[arg(long, short)]
    pub prefix: Vec<IpNet>,
}

#[derive(Debug, Clone, Parser)]
pub struct SetDscpMarkingCmd {
    /// DSCP module name to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,

    /// Dataplane instances where the changes should be applied, optional.
    ///
    /// If no instances specified, the route will be applied to all instances.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,

    /// DSCP marking flag: 0 - Never, 1 - Default (only if original DSCP is 0), 2 - Always
    #[arg(long)]
    pub flag: u32,

    /// DSCP mark value (0-63)
    #[arg(long)]
    pub mark: u32,
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
    let mut service = DscpService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::PrefixAdd(cmd) => service.add_prefixes(cmd).await,
        ModeCmd::PrefixRemove(cmd) => service.remove_prefixes(cmd).await,
        ModeCmd::SetMarking(cmd) => service.set_dscp_marking(cmd).await,
    }
}

pub struct DscpService {
    client: DscpServiceClient<Channel>,
}

impl DscpService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = DscpServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name.to_owned(),
                instances: Vec::new(),
            }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn add_prefixes(&mut self, cmd: AddPrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = AddPrefixesRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.unwrap_or_default(),
            }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("AddPrefixesRequest: {:?}", request);
        let response = self.client.add_prefixes(request).await?.into_inner();
        log::debug!("AddPrefixesResponse: {:?}", response);
        Ok(())
    }

    pub async fn remove_prefixes(&mut self, cmd: RemovePrefixesCmd) -> Result<(), Box<dyn Error>> {
        let request = RemovePrefixesRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.unwrap_or_default(),
            }),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("RemovePrefixesRequest: {:?}", request);
        let response = self.client.remove_prefixes(request).await?.into_inner();
        log::debug!("RemovePrefixesResponse: {:?}", response);
        Ok(())
    }

    pub async fn set_dscp_marking(&mut self, cmd: SetDscpMarkingCmd) -> Result<(), Box<dyn Error>> {
        // Validate flag value
        if cmd.flag > 2 {
            return Err("Invalid flag value (must be 0, 1, or 2)".into());
        }

        // Validate mark value (6-bit field)
        if cmd.mark > 63 {
            return Err("Invalid mark value (must be 0-63)".into());
        }

        let request = SetDscpMarkingRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.unwrap_or_default(),
            }),
            dscp_config: Some(DscpConfig {
                flag: cmd.flag,
                mark: cmd.mark,
            }),
        };
        log::trace!("SetDscpMarkingRequest: {:?}", request);
        let response = self.client.set_dscp_marking(request).await?.into_inner();
        log::debug!("SetDscpMarkingResponse: {:?}", response);
        Ok(())
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("DSCP Configs".to_string());

    for config in &resp.configs {
        tree.begin_child(format!("Instance {}", config.instance));

        if let Some(dscp_config) = &config.dscp_config {
            tree.begin_child("DSCP Marking".to_string());
            tree.add_empty_child(format!("Flag: {}", flag_to_string(dscp_config.flag)));
            tree.add_empty_child(format!(
                "Mark: {} (0x{:02x})",
                dscp_config.mark, dscp_config.mark
            ));
            tree.end_child();
        }

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

fn flag_to_string(flag: u32) -> String {
    match flag {
        0 => "Never".to_string(),
        1 => "Default (only if original DSCP is 0)".to_string(),
        2 => "Always".to_string(),
        _ => format!("Unknown ({})", flag),
    }
}
