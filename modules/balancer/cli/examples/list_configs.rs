//! Example: list-configs command output
//! Run with: cargo run --example list_configs [format]
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::env;
use std::error::Error;
use yanet_cli_balancer::output::{self, OutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let response = common::create_list_configs_example();
    
    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());
    
    match format {
        Some("table") => {
            output::print_list_configs(&response, OutputFormat::Table)?;
        }
        Some("tree") => {
            output::print_list_configs(&response, OutputFormat::Tree)?;
        }
        Some("json") => {
            output::print_list_configs(&response, OutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== list-configs: Table format ===\n");
            output::print_list_configs(&response, OutputFormat::Table)?;
            
            println!("\n\n=== list-configs: Tree format ===\n");
            output::print_list_configs(&response, OutputFormat::Tree)?;
            
            println!("\n\n=== list-configs: JSON format ===\n");
            output::print_list_configs(&response, OutputFormat::Json)?;
        }
    }
    
    Ok(())
}