#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Scapy AST Parser - Parses gen.py files using Python AST to extract packet definitions.
Outputs structured JSON IR for Go code generation.
"""

import ast
import json
import sys
from typing import Any, Dict, List, Optional, Union


class PacketLayer:
    """Represents a single layer in a packet (e.g., Ether, IP, TCP)"""
    def __init__(self, layer_type: str, params: Dict[str, Any]):
        self.layer_type = layer_type
        self.params = params
    
    def to_dict(self):
        return {
            "type": self.layer_type,
            "params": self.params
        }


class PacketDefinition:
    """Represents a complete packet definition"""
    def __init__(self, layers: List[PacketLayer], special_handling: Optional[Dict] = None):
        self.layers = layers
        self.special_handling = special_handling
    
    def to_dict(self):
        return {
            "layers": [layer.to_dict() for layer in self.layers],
            "special_handling": self.special_handling
        }


class PCAPPair:
    """Represents a send/expect PCAP file pair"""
    def __init__(self, send_file: str, expect_file: str, 
                 send_packets: List[PacketDefinition], 
                 expect_packets: List[PacketDefinition]):
        self.send_file = send_file
        self.expect_file = expect_file
        self.send_packets = send_packets
        self.expect_packets = expect_packets
    
    def to_dict(self):
        return {
            "send_file": self.send_file,
            "expect_file": self.expect_file,
            "send_packets": [pkt.to_dict() for pkt in self.send_packets],
            "expect_packets": [pkt.to_dict() for pkt in self.expect_packets]
        }


class ScapyASTParser(ast.NodeVisitor):
    """AST visitor that extracts Scapy packet definitions from gen.py files"""
    
    def __init__(self, verbose: bool = False):
        self.verbose = verbose
        self.write_pcap_calls: List[Dict] = []
        self.helper_functions: Dict[str, ast.FunctionDef] = {}
        self.current_function: Optional[str] = None
        self.variables: Dict[str, Any] = {}  # Store variable assignments
    
    def parse_file(self, filepath: str) -> Dict:
        """Parse a gen.py file and return structured IR"""
        with open(filepath, 'r', encoding='utf-8') as f:
            content = f.read()
        
        tree = ast.parse(content, filename=filepath)
        self.visit(tree)
        
        # Process write_pcap calls and build PCAP pairs
        pcap_pairs = self._build_pcap_pairs()
        
        return {
            "pcap_pairs": [pair.to_dict() for pair in pcap_pairs],
            "helper_functions": list(self.helper_functions.keys())
        }
    
    def visit_FunctionDef(self, node: ast.FunctionDef):
        """Visit function definitions to extract helper functions"""
        # Skip write_pcap definition itself (it's the main function)
        if node.name == 'write_pcap':
            return

        # Store ALL other functions as potential helper functions
        # This includes ipv4_packet1(), ipv6_packet1(), etc.
        self.helper_functions[node.name] = node
        if self.verbose:
            print(f"Found helper function: {node.name}")

        # Don't visit children to avoid processing write_pcap calls inside function definitions
        # (if a helper calls write_pcap, we don't want to extract that call)
        return
    
    def visit_Assign(self, node: ast.Assign):
        """Visit assignments to track variables like ipv4_fragments1 = fragment(...)"""
        for target in node.targets:
            if isinstance(target, ast.Name):
                var_name = target.id
                # Store the AST node for later resolution
                self.variables[var_name] = node.value
                if self.verbose:
                    print(f"Found variable assignment: {var_name}")
        
        self.generic_visit(node)
    
    def visit_Call(self, node: ast.Call):
        """Visit function calls to extract write_pcap calls"""
        func_name = self._get_call_name(node)
        
        # Handle write_pcap calls
        if func_name == 'write_pcap':
            call_info = self._extract_write_pcap_call(node)
            if call_info:
                self.write_pcap_calls.append(call_info)
                if self.verbose:
                    print(f"Found write_pcap call: {call_info['filename']}")
        
        # Handle write_pcap_stepN or other helper function calls that write pcaps
        elif func_name and func_name.startswith('write_pcap') and func_name != 'write_pcap':
            call_info = self._extract_helper_write_pcap_call(node)
            if call_info:
                self.write_pcap_calls.append(call_info)
                if self.verbose:
                    print(f"Found helper write_pcap call: {func_name}")
        
        self.generic_visit(node)
    
    def _extract_write_pcap_call(self, node: ast.Call) -> Optional[Dict]:
        """Extract filename and packet definitions from write_pcap call"""
        if not node.args:
            return None
        
        # First argument is filename
        filename_node = node.args[0]
        filename = self._eval_node(filename_node)
        if not isinstance(filename, str):
            return None
        
        # Remaining arguments are packets
        packets = []
        for packet_node in node.args[1:]:
            packet_defs = self._extract_packet(packet_node)
            packets.extend(packet_defs)
        
        return {
            "filename": filename,
            "packets": packets
        }
    
    def _extract_helper_write_pcap_call(self, node: ast.Call) -> Optional[Dict]:
        """Extract write_pcap call from helper function like write_pcap_step1"""
        func_name = self._get_call_name(node)
        helper_func = self.helper_functions.get(func_name)
        
        if not helper_func or not node.args:
            return None
        
        # First argument should be filename
        filename_arg = node.args[0]
        filename = self._eval_node(filename_arg)
        if not isinstance(filename, str):
            return None
        
        # Find the write_pcap call inside the helper function
        # and extract packets from it, substituting the filename parameter
        for stmt in ast.walk(helper_func):
            if isinstance(stmt, ast.Call) and isinstance(stmt.func, ast.Name) and stmt.func.id == 'write_pcap':
                # Extract packets from this call
                packets = []
                for packet_node in stmt.args[1:]:  # Skip filename argument
                    packet_defs = self._extract_packet(packet_node)
                    packets.extend(packet_defs)
                
                return {
                    "filename": filename,
                    "packets": packets
                }
        
        return None
    
    def _extract_packet(self, node: ast.AST) -> List[PacketDefinition]:
        """Extract packet definition(s) from an AST node"""
        # Handle different packet construction patterns
        
        # 1. Direct layer chain: Ether()/IP()/TCP()
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Div):
            return [self._parse_layer_chain(node)]
        
        # 2. Function call that returns packet(s)
        if isinstance(node, ast.Call):
            func_name = self._get_call_name(node)
            
            # fragment() or fragment6() - returns list of packets
            if func_name in ['fragment', 'fragment6']:
                return self._handle_fragmentation(node)
            
            # Helper function call
            if func_name in self.helper_functions:
                return self._handle_helper_function(node)
            
            # Single layer (starting point)
            if func_name in ['Ether', 'IP', 'IPv6', 'TCP', 'UDP', 'ICMP', 'ICMPv6EchoRequest', 
                             'ICMPv6EchoReply', 'ICMPv6DestUnreach', 'Raw', 'Dot1Q', 'GRE', 'MPLS']:
                return [self._parse_single_layer_packet(node)]
        
        # 3. Array subscript: fragment()[0]
        if isinstance(node, ast.Subscript):
            return self._handle_subscript(node)
        
        if self.verbose:
            print(f"Warning: Unhandled packet node type: {type(node).__name__}")
        return []
    
    def _parse_layer_chain(self, node: ast.BinOp) -> PacketDefinition:
        """Parse a chain of layers: Ether()/IP()/TCP() or with subscripts"""
        layers = []

        def collect_layers(n: ast.AST):
            if isinstance(n, ast.BinOp) and isinstance(n.op, ast.Div):
                collect_layers(n.left)
                collect_layers(n.right)
            elif isinstance(n, ast.BinOp) and isinstance(n.op, ast.Mult):
                # Handle string multiplication for payload: "ABC"*100
                if isinstance(n.left, ast.Constant) and isinstance(n.right, ast.Constant):
                    content = n.left.value
                    count = n.right.value
                    if isinstance(content, str) and isinstance(count, int):
                        # Create a Raw layer with special handling for multiplication
                        raw_layer = PacketLayer(
                            layer_type="Raw",
                            params={
                                "_arg0": content * count,  # Pre-multiply for now
                                "_special": {
                                    "payload": {
                                        "type": "string_mult",
                                        "content": content,
                                        "count": count
                                    }
                                }
                            }
                        )
                        layers.append(raw_layer)
            elif isinstance(n, ast.Call):
                layer = self._parse_layer_call(n)
                if layer:
                    layers.append(layer)
            elif isinstance(n, ast.Name) and isinstance(n.ctx, ast.Load):
                # Handle helper function calls like ipv4_packet1()/IP()
                # Get the function definition and evaluate its return statement
                helper_packets = self._handle_helper_function(n.id)
                if helper_packets and helper_packets[0].layers:
                    # Add all layers from the helper function instead of the name
                    layers.extend(helper_packets[0].layers)
                # Skip adding the helper function name as a layer - do nothing else
                pass
            elif isinstance(n, ast.Subscript):
                # Handle subscript to variable like ipv4_fragments1[0]
                # This represents a Raw payload layer
                packets = self._handle_subscript(n)
                if packets and packets[0].layers:
                    # Add all layers from the subscripted packet as payload
                    layers.extend(packets[0].layers)
            elif isinstance(n, (ast.Constant, ast.Str)):
                # Handle string literal payload: ICMPv6EchoRequest()/"payload string"
                # Extract the string value
                if isinstance(n, ast.Constant):
                    payload_str = n.value
                else:  # ast.Str (Python < 3.8)
                    payload_str = n.s

                if isinstance(payload_str, str):
                    # Create a Raw layer with the payload
                    raw_layer = PacketLayer(
                        layer_type="Raw",
                        params={"_arg0": payload_str}
                    )
                    layers.append(raw_layer)

        collect_layers(node)
        return PacketDefinition(layers)
    
    def _parse_single_layer_packet(self, node: ast.Call) -> PacketDefinition:
        """Parse a packet that's just a single layer call"""
        layer = self._parse_layer_call(node)
        return PacketDefinition([layer] if layer else [])
    
    def _parse_layer_call(self, node: ast.Call) -> Optional[PacketLayer]:
        """Parse a single layer constructor call"""
        layer_type = self._get_call_name(node)
        if not layer_type:
            return None

        # Skip helper functions - they should be handled by _handle_helper_function
        if layer_type in self.helper_functions:
            return None
        
        params = {}
        special_handling = {}
        
        # Extract keyword arguments
        for keyword in node.keywords:
            key = keyword.arg
            
            # Check for port ranges (tuples)
            if isinstance(keyword.value, ast.Tuple) and len(keyword.value.elts) == 2:
                # This is a port range: sport=(1024, 1040)
                start = self._eval_node(keyword.value.elts[0])
                end = self._eval_node(keyword.value.elts[1])
                special_handling[key] = {
                    "type": "port_range",
                    "range": [start, end]
                }
            # Check for arrays (lists) - for parameter iteration
            elif isinstance(keyword.value, ast.List):
                # This is a parameter array: code=[0,1,3,5]
                values = [self._eval_node(elem) for elem in keyword.value.elts]
                special_handling[key] = {
                    "type": "param_array",
                    "values": values
                }
            else:
                value = self._eval_node(keyword.value)
                # Check for CIDR notation in IP addresses
                if isinstance(value, str) and '/' in value and (key in ['src', 'dst']):
                    # This is CIDR notation: Scapy generates packets for all IPs in subnet
                    special_handling[key] = {
                        "type": "cidr_expansion",
                        "cidr": value
                    }
                    # Strip CIDR suffix and keep only the base IP for the layer params
                    params[key] = value.split('/')[0]
                else:
                    params[key] = value
        
        # Extract positional arguments (rare in Scapy, but handle payload strings)
        for i, arg in enumerate(node.args):
            value = self._eval_node(arg)
            
            # Handle string multiplication for payload: "ABC"*100
            if isinstance(arg, ast.BinOp) and isinstance(arg.op, ast.Mult):
                if isinstance(arg.left, ast.Constant) and isinstance(arg.right, ast.Constant):
                    special_handling["payload"] = {
                        "type": "string_mult",
                        "content": arg.left.value,
                        "count": arg.right.value
                    }
            elif isinstance(value, str):
                # Direct string payload
                params[f"_arg{i}"] = value
        
        layer = PacketLayer(layer_type, params)
        
        # If we have special handling, include it in the packet definition
        if special_handling:
            # We need to return this at packet level, not layer level
            # For now, store it in params with a special key
            params["_special"] = special_handling
        
        return layer
    
    def _handle_fragmentation(self, node: ast.Call) -> List[PacketDefinition]:
        """Handle fragment() or fragment6() function calls"""
        func_name = self._get_call_name(node)
        
        if not node.args:
            return []
        
        # First argument is the packet to fragment
        base_packet_node = node.args[0]
        base_packet_defs = self._extract_packet(base_packet_node)
        
        if not base_packet_defs:
            return []
        
        base_packet = base_packet_defs[0]
        
        # Extract fragSize parameter
        frag_size = None
        for keyword in node.keywords:
            if keyword.arg in ['fragSize', 'fragsize']:
                frag_size = self._eval_node(keyword.value)
        
        # Mark packet with fragmentation special handling
        base_packet.special_handling = {
            "type": func_name,
            "frag_size": frag_size,
            "fragment_index": None  # Will generate all fragments
        }
        
        return [base_packet]
    
    def _handle_subscript(self, node: ast.Subscript) -> List[PacketDefinition]:
        """Handle array subscript like fragment()[0] or ipv4_fragments1[0]"""
        # Extract index
        index = self._eval_node(node.slice)
        
        # Case 1: Direct function call with subscript - fragment()[0]
        if isinstance(node.value, ast.Call):
            packets = self._extract_packet(node.value)
            
            if packets and packets[0].special_handling:
                packets[0].special_handling["index"] = index
            
            return packets
        
        # Case 2: Variable reference with subscript - ipv4_fragments1[0]
        elif isinstance(node.value, ast.Name):
            var_name = node.value.id
            if var_name in self.variables:
                # Resolve the variable and extract packet
                var_value = self.variables[var_name]
                packets = self._extract_packet(var_value)
                
                if packets and packets[0].special_handling:
                    packets[0].special_handling["index"] = index
                
                return packets
        
        return []
    
    def _handle_helper_function(self, node_or_name: Union[ast.Call, str]) -> List[PacketDefinition]:
        """Handle calls to helper functions like ipv4_send() or ipv4_packet1()"""
        if isinstance(node_or_name, ast.Call):
            func_name = self._get_call_name(node_or_name)
        else:
            func_name = node_or_name

        helper_func = self.helper_functions.get(func_name)

        if not helper_func:
            return []

        # For helper functions without parameters, just evaluate the return statement
        # This handles functions like ipv4_packet1() that return Ether()/IP()/TCP()

        for stmt in helper_func.body:
            if isinstance(stmt, ast.Return) and stmt.value:
                return self._extract_packet(stmt.value)

        return []
    
    def _get_call_name(self, node: ast.Call) -> Optional[str]:
        """Get the function name from a call node"""
        if isinstance(node.func, ast.Name):
            return node.func.id
        elif isinstance(node.func, ast.Attribute):
            return node.func.attr
        return None
    
    def _eval_node(self, node: ast.AST) -> Any:
        """Safely evaluate an AST node to get its value"""
        try:
            value = ast.literal_eval(node)
            # Handle CIDR notation - keep full CIDR string for special processing
            # Scapy generates packets for all IPs in the subnet when CIDR is used
            return value
        except (ValueError, TypeError):
            # Handle more complex expressions
            if isinstance(node, ast.Name):
                # Try to resolve variable
                if node.id in self.variables:
                    return self._eval_node(self.variables[node.id])
                # Return variable name as placeholder
                return f"VAR_{node.id}"
            elif isinstance(node, ast.BinOp):
                if isinstance(node.op, ast.Mult):
                    left = self._eval_node(node.left)
                    right = self._eval_node(node.right)
                    if isinstance(left, str) and isinstance(right, int):
                        return left * right
                elif isinstance(node.op, ast.Div):
                    # Handle CIDR notation like "90.90.90.0/30"
                    # Return just the base address without /mask
                    left = self._eval_node(node.left)
                    return left
            elif isinstance(node, ast.Call):
                # Function call - return a placeholder
                func_name = self._get_call_name(node)
                return f"CALL_{func_name}"
            
            # Return a string representation as fallback
            result = ast.unparse(node) if hasattr(ast, 'unparse') else str(node)
            # Strip CIDR from result too
            if isinstance(result, str) and '/' in result:
                result = result.split('/')[0]
            return result
    
    def _build_pcap_pairs(self) -> List[PCAPPair]:
        """Build PCAP pairs from write_pcap calls"""
        # Group by filename pattern (send/expect pairs)
        pcap_files: Dict[str, Dict[str, Any]] = {}
        
        for call in self.write_pcap_calls:
            filename = call["filename"]
            packets = call["packets"]
            
            # Determine if this is a send or expect file
            is_send = "send" in filename.lower() or (not "expect" in filename.lower() and filename != "expect.pcap")
            is_expect = "expect" in filename.lower() or filename == "expect.pcap"
            
            # Extract base name for pairing
            base_name = self._get_base_name(filename)
            
            if base_name not in pcap_files:
                pcap_files[base_name] = {
                    "send_file": None,
                    "expect_file": None,
                    "send_packets": [],
                    "expect_packets": []
                }
            
            if is_send:
                pcap_files[base_name]["send_file"] = filename
                pcap_files[base_name]["send_packets"] = packets
            if is_expect:
                pcap_files[base_name]["expect_file"] = filename
                pcap_files[base_name]["expect_packets"] = packets
        
        # Convert to PCAPPair objects
        pairs = []
        for base_name, data in pcap_files.items():
            pair = PCAPPair(
                send_file=data["send_file"] or "",
                expect_file=data["expect_file"] or "",
                send_packets=data["send_packets"],
                expect_packets=data["expect_packets"]
            )
            pairs.append(pair)
        
        return pairs
    
    def _get_base_name(self, filename: str) -> str:
        """Extract base name from PCAP filename for pairing"""
        filename = filename.replace(".pcap", "")
        
        if filename.endswith("-send"):
            return filename[:-5]
        elif filename.endswith("-expect"):
            return filename[:-7]
        elif filename.endswith("_send"):
            return filename[:-5]
        elif filename.endswith("_expect"):
            return filename[:-7]
        elif filename == "send":
            return "default"
        elif filename == "expect":
            return "default"
        else:
            return filename


def main():
    """Command-line interface"""
    if len(sys.argv) < 2:
        print("Usage: scapy_ast_parser.py <gen.py file> [--verbose]")
        sys.exit(1)
    
    filepath = sys.argv[1]
    verbose = "--verbose" in sys.argv or "-v" in sys.argv
    
    parser = ScapyASTParser(verbose=verbose)
    ir = parser.parse_file(filepath)
    
    # Output JSON
    print(json.dumps(ir, indent=2))


if __name__ == "__main__":
    main()

