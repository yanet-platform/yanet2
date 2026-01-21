//! Example: state command output
//! Run with: cargo run --example state_info [format]
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let response = common::create_state_info_example();

    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());

    match format {
        Some("table") => {
            output::print_show_info(&response, OutputFormat::Table)?;
        }
        Some("tree") => {
            output::print_show_info(&response, OutputFormat::Tree)?;
        }
        Some("json") => {
            output::print_show_info(&response, OutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== state: Table format ===\n");
            output::print_show_info(&response, OutputFormat::Table)?;

            println!("\n\n=== state: Tree format ===\n");
            output::print_show_info(&response, OutputFormat::Tree)?;

            println!("\n\n=== state: JSON format ===\n");
            output::print_show_info(&response, OutputFormat::Json)?;
        }
    }

    Ok(())
}
