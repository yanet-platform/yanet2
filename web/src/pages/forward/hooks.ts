import type { Rule, IPNet, VlanRange, Device } from '../../api/forward';
import { ForwardMode, FORWARD_MODE_LABELS } from '../../api/forward';
import type { RuleItem } from './types';
import {
    bytesToBase64,
    formatIPNet,
    parseIPToBytes,
    prefixLengthToMaskBytes,
} from '../../utils';

/**
 * Convert rules array to RuleItem array with unique ids
 */
export const rulesToItems = (rules: Rule[]): RuleItem[] => {
    return rules.map((rule, index) => ({
        id: `rule-${index}`,
        index,
        rule,
    }));
};

/**
 * Format devices array to display string
 */
export const formatDevices = (devices: Device[] | undefined): string => {
    if (!devices || devices.length === 0) return '*';
    return devices.map((d) => d.name || '').filter(Boolean).join(', ') || '*';
};

/**
 * Format VLAN ranges array to display string
 */
export const formatVlanRanges = (ranges: VlanRange[] | undefined): string => {
    if (!ranges || ranges.length === 0) return '*';
    return ranges.map((r) => {
        const from = r.from ?? 0;
        const to = r.to ?? 0;
        if (from === to) return String(from);
        return `${from}-${to}`;
    }).join(', ');
};

/**
 * Format IPNet array to display string
 */
export const formatIPNets = (nets: IPNet[] | undefined): string => {
    if (!nets || nets.length === 0) return '*';
    return nets.map((net) => {
        // Extract bytes from addr and mask
        const addrBytes = extractBytes(net.addr);
        const maskBytes = extractBytes(net.mask);
        if (!addrBytes || addrBytes.length === 0) return '';
        return formatIPNet(addrBytes, maskBytes);
    }).filter(Boolean).join(', ') || '*';
};

/**
 * Extract bytes array from various formats
 */
const extractBytes = (data: string | Uint8Array | number[] | undefined): number[] | undefined => {
    if (!data) return undefined;
    if (Array.isArray(data)) return data;
    if (data instanceof Uint8Array) return Array.from(data);
    // Base64 encoded string
    if (typeof data === 'string') {
        try {
            const binary = atob(data);
            return Array.from(binary, (c) => c.charCodeAt(0));
        } catch {
            return undefined;
        }
    }
    return undefined;
};

/**
 * Format forward mode to display string
 */
export const formatMode = (mode: ForwardMode | undefined): string => {
    return FORWARD_MODE_LABELS[mode ?? ForwardMode.NONE];
};

/**
 * Parse comma-separated string to Device array
 */
export const parseDevices = (input: string): Device[] => {
    if (!input.trim()) return [];
    return input.split(',').map((s) => s.trim()).filter(Boolean).map((name) => ({ name }));
};

/**
 * Parse VLAN ranges string (e.g., "1-100, 200, 300-400") to VlanRange array
 */
export const parseVlanRanges = (input: string): VlanRange[] => {
    if (!input.trim()) return [];
    return input.split(',').map((s) => s.trim()).filter(Boolean).map((part) => {
        if (part.includes('-')) {
            const [fromStr, toStr] = part.split('-');
            return { from: parseInt(fromStr, 10), to: parseInt(toStr, 10) };
        }
        const val = parseInt(part, 10);
        return { from: val, to: val };
    }).filter((r) => !isNaN(r.from ?? NaN) && !isNaN(r.to ?? NaN));
};

/**
 * Parse comma-separated CIDR prefixes to IPNet array with base64-encoded bytes
 */
export const parsePrefixesToIPNets = (input: string): IPNet[] => {
    if (!input.trim()) return [];

    const results: IPNet[] = [];

    for (const part of input.split(',')) {
        const prefix = part.trim();
        if (!prefix) continue;

        const parts = prefix.split('/');
        if (parts.length !== 2) continue;

        const [ipPart, maskStr] = parts;
        const prefixLength = parseInt(maskStr, 10);
        if (isNaN(prefixLength)) continue;

        const addrBytes = parseIPToBytes(ipPart);
        if (!addrBytes) continue;

        const isIPv4 = addrBytes.length === 4;
        const maxPrefix = isIPv4 ? 32 : 128;
        if (prefixLength < 0 || prefixLength > maxPrefix) continue;

        const maskBytes = prefixLengthToMaskBytes(prefixLength, isIPv4 ? 4 : 16);

        results.push({
            addr: bytesToBase64(addrBytes),
            mask: bytesToBase64(maskBytes),
        });
    }

    return results;
};
