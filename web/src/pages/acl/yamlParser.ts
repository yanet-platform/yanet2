import yaml from 'js-yaml';
import type { Rule, IPNet, PortRange, ProtoRange, VlanRange, ActionKind } from '../../api/acl';
import type { YamlAclConfig, YamlAclRule } from './types';

// Convert bytes array to base64 string for protobuf
const bytesToBase64 = (bytes: number[]): string => {
    const uint8 = new Uint8Array(bytes);
    let binary = '';
    for (let i = 0; i < uint8.length; i++) {
        binary += String.fromCharCode(uint8[i]);
    }
    return btoa(binary);
};

// Convert base64 string to bytes array
const base64ToBytes = (base64: string): number[] => {
    try {
        const binary = atob(base64);
        const bytes: number[] = [];
        for (let i = 0; i < binary.length; i++) {
            bytes.push(binary.charCodeAt(i));
        }
        return bytes;
    } catch {
        return [];
    }
};

// Parse IP address string to bytes array
const parseIPAddress = (ipStr: string): number[] => {
    // Check if it's IPv6
    if (ipStr.includes(':')) {
        return parseIPv6(ipStr);
    }
    // IPv4
    return parseIPv4(ipStr);
};

const parseIPv4 = (ipStr: string): number[] => {
    const parts = ipStr.split('.');
    if (parts.length !== 4) {
        throw new Error(`Invalid IPv4 address: ${ipStr}`);
    }
    return parts.map(p => {
        const num = parseInt(p, 10);
        if (isNaN(num) || num < 0 || num > 255) {
            throw new Error(`Invalid IPv4 octet: ${p}`);
        }
        return num;
    });
};

const parseIPv6 = (ipStr: string): number[] => {
    // Handle :: expansion
    let fullAddr = ipStr;
    if (ipStr.includes('::')) {
        const parts = ipStr.split('::');
        const left = parts[0] ? parts[0].split(':') : [];
        const right = parts[1] ? parts[1].split(':') : [];
        const missing = 8 - left.length - right.length;
        const middle = Array(missing).fill('0');
        fullAddr = [...left, ...middle, ...right].join(':');
    }

    const parts = fullAddr.split(':');
    if (parts.length !== 8) {
        throw new Error(`Invalid IPv6 address: ${ipStr}`);
    }

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part || '0', 16);
        if (isNaN(num) || num < 0 || num > 0xffff) {
            throw new Error(`Invalid IPv6 part: ${part}`);
        }
        bytes.push((num >> 8) & 0xff);
        bytes.push(num & 0xff);
    }
    return bytes;
};

// Parse CIDR notation or IP/mask notation - returns base64 encoded bytes
const parseIPNetToBase64 = (netStr: string): IPNet => {
    const parts = netStr.split('/');
    
    if (parts.length === 1) {
        // Single IP address - use full mask
        const addr = parseIPAddress(netStr);
        const mask = addr.length === 4 
            ? [255, 255, 255, 255]
            : Array(16).fill(255);
        return { 
            addr: bytesToBase64(addr), 
            mask: bytesToBase64(mask),
        };
    }

    if (parts.length !== 2) {
        throw new Error(`Invalid network format: ${netStr}`);
    }

    const addr = parseIPAddress(parts[0]);
    
    // Check if second part is a prefix length or a mask
    if (parts[1].includes('.') || parts[1].includes(':')) {
        // It's a mask
        const mask = parseIPAddress(parts[1]);
        return { 
            addr: bytesToBase64(addr), 
            mask: bytesToBase64(mask),
        };
    } else {
        // It's a prefix length
        const prefixLen = parseInt(parts[1], 10);
        const maxBits = addr.length === 4 ? 32 : 128;
        
        if (isNaN(prefixLen) || prefixLen < 0 || prefixLen > maxBits) {
            throw new Error(`Invalid prefix length: ${parts[1]}`);
        }

        // Create mask from prefix length
        const mask: number[] = [];
        let remaining = prefixLen;
        for (let i = 0; i < addr.length; i++) {
            if (remaining >= 8) {
                mask.push(255);
                remaining -= 8;
            } else if (remaining > 0) {
                mask.push((0xff << (8 - remaining)) & 0xff);
                remaining = 0;
            } else {
                mask.push(0);
            }
        }
        return { 
            addr: bytesToBase64(addr), 
            mask: bytesToBase64(mask),
        };
    }
};

// Convert YAML rule to API Rule format
const convertYamlRule = (yamlRule: YamlAclRule): Rule => {
    const srcs: IPNet[] = (yamlRule.srcs || []).map(parseIPNetToBase64);
    const dsts: IPNet[] = (yamlRule.dsts || []).map(parseIPNetToBase64);

    const srcPortRanges: PortRange[] = (yamlRule.src_ports || []).map(r => ({
        from: r.from,
        to: r.to,
    }));

    const dstPortRanges: PortRange[] = (yamlRule.dst_ports || []).map(r => ({
        from: r.from,
        to: r.to,
    }));

    const protoRanges: ProtoRange[] = (yamlRule.proto_ranges || []).map(r => ({
        from: r.from,
        to: r.to,
    }));

    const vlanRanges: VlanRange[] = (yamlRule.vlan_ranges || []).map(r => ({
        from: r.from,
        to: r.to,
    }));

    const action: ActionKind = yamlRule.action === 'Allow' ? 0 : 1;

    return {
        srcs,
        dsts,
        srcPortRanges,
        dstPortRanges,
        protoRanges,
        vlanRanges,
        devices: yamlRule.devices || [],
        counter: yamlRule.counter || '',
        action,
        keepState: false,
    };
};

// Parse YAML content and convert to Rules
export const parseYamlConfig = (content: string): Rule[] => {
    const config = yaml.load(content) as YamlAclConfig;
    
    if (!config || !Array.isArray(config.rules)) {
        throw new Error('Invalid YAML format: expected object with "rules" array');
    }

    return config.rules.map((rule, index) => {
        try {
            return convertYamlRule(rule);
        } catch (err) {
            throw new Error(`Error parsing rule #${index + 1}: ${err instanceof Error ? err.message : String(err)}`);
        }
    });
};

// Check if mask is contiguous (all 1s followed by all 0s)
const isContiguousMask = (maskBytes: number[]): boolean => {
    let foundZero = false;
    for (const byte of maskBytes) {
        for (let bit = 7; bit >= 0; bit--) {
            const isSet = (byte & (1 << bit)) !== 0;
            if (foundZero && isSet) {
                return false; // Found 1 after 0 - non-contiguous
            }
            if (!isSet) {
                foundZero = true;
            }
        }
    }
    return true;
};

// Count prefix length from contiguous mask
const countPrefixLength = (maskBytes: number[]): number => {
    let prefixLen = 0;
    for (const byte of maskBytes) {
        for (let bit = 7; bit >= 0; bit--) {
            if (byte & (1 << bit)) {
                prefixLen++;
            } else {
                return prefixLen;
            }
        }
    }
    return prefixLen;
};

// Format IPv6 address with :: compression
const formatIPv6Address = (bytes: number[]): string => {
    if (bytes.length !== 16) return '';
    
    // Build array of 16-bit parts
    const parts: number[] = [];
    for (let i = 0; i < 16; i += 2) {
        parts.push((bytes[i] << 8) | bytes[i + 1]);
    }
    
    // Find longest run of zeros for :: compression
    let longestStart = -1;
    let longestLen = 0;
    let currentStart = -1;
    let currentLen = 0;
    
    for (let i = 0; i < 8; i++) {
        if (parts[i] === 0) {
            if (currentStart === -1) {
                currentStart = i;
                currentLen = 1;
            } else {
                currentLen++;
            }
        } else {
            if (currentLen > longestLen && currentLen > 1) {
                longestStart = currentStart;
                longestLen = currentLen;
            }
            currentStart = -1;
            currentLen = 0;
        }
    }
    // Check at end
    if (currentLen > longestLen && currentLen > 1) {
        longestStart = currentStart;
        longestLen = currentLen;
    }
    
    // Build string
    if (longestStart === -1) {
        // No compression
        return parts.map(p => p.toString(16)).join(':');
    }
    
    const left = parts.slice(0, longestStart).map(p => p.toString(16)).join(':');
    const right = parts.slice(longestStart + longestLen).map(p => p.toString(16)).join(':');
    
    if (longestStart === 0 && longestLen === 8) {
        return '::';
    } else if (longestStart === 0) {
        return '::' + right;
    } else if (longestStart + longestLen === 8) {
        return left + '::';
    } else {
        return left + '::' + right;
    }
};

// Format IPv4 address
const formatIPv4Address = (bytes: number[]): string => {
    return bytes.join('.');
};

// Format IP bytes to human-readable string
export const formatIPNet = (net: IPNet): string => {
    if (!net.addr) return '';
    
    // Handle both base64 string and number array
    const addr = typeof net.addr === 'string' 
        ? base64ToBytes(net.addr) 
        : Array.from(net.addr);
    const mask = net.mask 
        ? (typeof net.mask === 'string' ? base64ToBytes(net.mask) : Array.from(net.mask))
        : null;
    
    if (addr.length === 0) return '';
    
    if (addr.length === 4) {
        // IPv4
        const ipStr = formatIPv4Address(addr);
        if (!mask) return ipStr;
        
        if (isContiguousMask(mask)) {
            const prefixLen = countPrefixLength(mask);
            return `${ipStr}/${prefixLen}`;
        } else {
            // Non-contiguous mask - show as IP/mask
            return `${ipStr}/${formatIPv4Address(mask)}`;
        }
    } else if (addr.length === 16) {
        // IPv6
        const ipStr = formatIPv6Address(addr);
        if (!mask) return ipStr;
        
        if (isContiguousMask(mask)) {
            const prefixLen = countPrefixLength(mask);
            return `${ipStr}/${prefixLen}`;
        } else {
            // Non-contiguous mask - show as IP/mask
            return `${ipStr}/${formatIPv6Address(mask)}`;
        }
    }
    
    return '';
};

// Format port range to string
export const formatPortRange = (range: PortRange): string => {
    if (range.from === range.to) {
        return String(range.from);
    }
    return `${range.from}-${range.to}`;
};

// Format proto range to string
export const formatProtoRange = (range: ProtoRange): string => {
    if (range.from === range.to) {
        return String(range.from);
    }
    return `${range.from}-${range.to}`;
};

// Format vlan range to string
export const formatVlanRange = (range: VlanRange): string => {
    if (range.from === range.to) {
        return String(range.from);
    }
    return `${range.from}-${range.to}`;
};
