#!/bin/bash

# Script to generate example outputs for all CLI commands in all formats
# Usage: ./generate_examples.sh

set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Generating example outputs for yanet-balancer CLI...${NC}"

# Create output directories
mkdir -p outputs/table
mkdir -p outputs/tree
mkdir -p outputs/json

# Build the project first
echo -e "${GREEN}Building project...${NC}"
cargo build --release 2>&1 | grep -v "Compiling\|Finished" || true

# List of examples
EXAMPLES=("config" "list" "stats" "sessions" "info" "graph")

# Generate outputs for each example in each format
for example in "${EXAMPLES[@]}"; do
    echo -e "${GREEN}Generating outputs for ${example}...${NC}"
    
    # Table format
    echo "  - table format"
    cargo run --example "$example" table 2>&1 | grep -v "Compiling\|Finished\|Running" > "outputs/table/${example}.txt"
    
    # Tree format
    echo "  - tree format"
    cargo run --example "$example" tree 2>&1 | grep -v "Compiling\|Finished\|Running" > "outputs/tree/${example}.txt"
    
    # JSON format
    echo "  - json format"
    cargo run --example "$example" json 2>&1 | grep -v "Compiling\|Finished\|Running" > "outputs/json/${example}.json"
done

echo -e "${BLUE}Done! Example outputs saved to:${NC}"
echo "  - outputs/table/"
echo "  - outputs/tree/"
echo "  - outputs/json/"