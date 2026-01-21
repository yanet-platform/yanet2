//! Example: graph command output
//! Run with: cargo run --example graph [format]
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat};
use yanet_cli_balancer::rpc::balancerpb;

fn main() -> Result<(), Box<dyn Error>> {
    let response = balancerpb::ShowGraphResponse {
        graph: Some(balancerpb::Graph {
            virtual_services: vec![
                balancerpb::GraphVs {
                    identifier: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 1] }),
                        port: 80,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    reals: vec![
                        balancerpb::GraphReal {
                            identifier: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 1] }),
                                port: 80,
                            }),
                            weight: 100,
                            effective_weight: 100,
                            enabled: true,
                        },
                        balancerpb::GraphReal {
                            identifier: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 1, 1, 2] }),
                                port: 80,
                            }),
                            weight: 50,
                            effective_weight: 50,
                            enabled: true,
                        },
                    ],
                },
                balancerpb::GraphVs {
                    identifier: Some(balancerpb::VsIdentifier {
                        addr: Some(balancerpb::Addr { bytes: vec![192, 0, 2, 2] }),
                        port: 443,
                        proto: balancerpb::TransportProto::Tcp as i32,
                    }),
                    reals: vec![
                        balancerpb::GraphReal {
                            identifier: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 1] }),
                                port: 443,
                            }),
                            weight: 100,
                            effective_weight: 90,
                            enabled: true,
                        },
                        balancerpb::GraphReal {
                            identifier: Some(balancerpb::RelativeRealIdentifier {
                                ip: Some(balancerpb::Addr { bytes: vec![10, 2, 1, 2] }),
                                port: 443,
                            }),
                            weight: 100,
                            effective_weight: 110,
                            enabled: true,
                        },
                    ],
                },
            ],
        }),
    };

    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());

    match format {
        Some("table") => {
            output::print_show_graph(&response, OutputFormat::Table)?;
        }
        Some("tree") => {
            output::print_show_graph(&response, OutputFormat::Tree)?;
        }
        Some("json") => {
            output::print_show_graph(&response, OutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== graph: Table format ===\n");
            output::print_show_graph(&response, OutputFormat::Table)?;

            println!("\n\n=== graph: Tree format ===\n");
            output::print_show_graph(&response, OutputFormat::Tree)?;

            println!("\n\n=== graph: JSON format ===\n");
            output::print_show_graph(&response, OutputFormat::Json)?;
        }
    }

    Ok(())
}
