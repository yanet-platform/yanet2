//! Example: sessions command output
//! Run with: cargo run --example sessions [format]
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let response = common::create_sessions_info_example();

    let args: Vec<String> = env::args().collect();
    let format = args.get(1).map(|s| s.as_str());

    match format {
        Some("table") => {
            output::print_show_sessions(&response, OutputFormat::Table)?;
        }
        Some("tree") => {
            output::print_show_sessions(&response, OutputFormat::Tree)?;
        }
        Some("json") => {
            output::print_show_sessions(&response, OutputFormat::Json)?;
        }
        Some(other) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        None => {
            println!("=== sessions: Table format ===\n");
            output::print_show_sessions(&response, OutputFormat::Table)?;

            println!("\n\n=== sessions: Tree format ===\n");
            output::print_show_sessions(&response, OutputFormat::Tree)?;

            println!("\n\n=== sessions: JSON format ===\n");
            output::print_show_sessions(&response, OutputFormat::Json)?;
        }
    }

    Ok(())
}
