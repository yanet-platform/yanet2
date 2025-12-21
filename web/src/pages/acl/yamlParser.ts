import yaml from 'js-yaml';
import type { Rule, IPNet, PortRange, ProtoRange, VlanRange, ActionKind } from '../../api/acl';
import type { YamlAclConfig, YamlAclRule } from './types';
import { bytesToBase64, getBytes } from '../../utils/bytes';
import {
    parseIPToBytes,
    formatIPv4FromBytes,
    formatIPv6FromBytes,
    isContiguousMask,
    countPrefixLength,
    prefixLengthToMaskBytes,
} from '../../utils/netip';

// Parse IP address string to bytes array (throws on error)
const parseIPAddressBytes = (ipStr: string): number[] => {
    const bytes = parseIPToBytes(ipStr);
    if (!bytes) {
        throw new Error(`Invalid IP address: ${ipStr}`);
    }
    return bytes;
};

// Parse CIDR notation or IP/mask notation - returns base64 encoded bytes
const parseIPNetToBase64 = (netStr: string): IPNet => {
    const parts = netStr.split('/');

    if (parts.length === 1) {
        // Single IP address - use full mask
        const addr = parseIPAddressBytes(netStr);
        const mask = addr.length === 4
            ? [255, 255, 255, 255]
            : Array(16).fill(255) as number[];
        return {
            addr: bytesToBase64(addr),
            mask: bytesToBase64(mask),
        };
    }

    if (parts.length !== 2) {
        throw new Error(`Invalid network format: ${netStr}`);
    }

    const addr = parseIPAddressBytes(parts[0]);

    // Check if second part is a prefix length or a mask
    if (parts[1].includes('.') || parts[1].includes(':')) {
        // It's a mask
        const mask = parseIPAddressBytes(parts[1]);
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

        const mask = prefixLengthToMaskBytes(prefixLen, addr.length);
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

    const srcPortRanges: PortRange[] = (yamlRule.src_ports || []).map((r) => ({
        from: r.from,
        to: r.to,
    }));

    const dstPortRanges: PortRange[] = (yamlRule.dst_ports || []).map((r) => ({
        from: r.from,
        to: r.to,
    }));

    const protoRanges: ProtoRange[] = (yamlRule.proto_ranges || []).map((r) => ({
        from: r.from,
        to: r.to,
    }));

    const vlanRanges: VlanRange[] = (yamlRule.vlan_ranges || []).map((r) => ({
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

// Format IP bytes to human-readable string
export const formatIPNet = (net: IPNet): string => {
    if (!net.addr) return '';

    const addr = getBytes(net.addr);
    const mask = net.mask ? getBytes(net.mask) : null;

    if (addr.length === 0) return '';

    if (addr.length === 4) {
        // IPv4
        const ipStr = formatIPv4FromBytes(addr);
        if (!mask) return ipStr;

        if (isContiguousMask(mask)) {
            const prefixLen = countPrefixLength(mask);
            return `${ipStr}/${prefixLen}`;
        } else {
            // Non-contiguous mask - show as IP/mask
            return `${ipStr}/${formatIPv4FromBytes(mask)}`;
        }
    } else if (addr.length === 16) {
        // IPv6
        const ipStr = formatIPv6FromBytes(addr);
        if (!mask) return ipStr;

        if (isContiguousMask(mask)) {
            const prefixLen = countPrefixLength(mask);
            return `${ipStr}/${prefixLen}`;
        } else {
            // Non-contiguous mask - show as IP/mask
            return `${ipStr}/${formatIPv6FromBytes(mask)}`;
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
