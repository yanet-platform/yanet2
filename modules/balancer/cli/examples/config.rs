//! Example: config command output
//! Run with: cargo run --example show_config [format]
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let response = common::create_show_config_example();

    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());

    match format {
        Some("table") => {
            output::print_show_config(&response, OutputFormat::Table)?;
        }
        Some("tree") => {
            output::print_show_config(&response, OutputFormat::Tree)?;
        }
        Some("json") => {
            output::print_show_config(&response, OutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== config: Table format ===\n");
            output::print_show_config(&response, OutputFormat::Table)?;

            println!("\n\n=== config: Tree format ===\n");
            output::print_show_config(&response, OutputFormat::Tree)?;

            println!("\n\n=== config: JSON format ===\n");
            output::print_show_config(&response, OutputFormat::Json)?;
        }
    }

    Ok(())
}
