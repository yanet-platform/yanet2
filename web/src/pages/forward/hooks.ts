import { useState, useLayoutEffect } from 'react';
import type { Rule, IPNet, VlanRange, Device } from '../../api/forward';
import { ForwardMode, FORWARD_MODE_LABELS } from '../../api/forward';
import type { RuleItem } from './types';
import { formatIPNet } from '../../utils';

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
 * Hook for measuring container height
 */
export const useContainerHeight = (containerRef: React.RefObject<HTMLDivElement | null>) => {
    const [containerHeight, setContainerHeight] = useState(0);

    useLayoutEffect(() => {
        const updateHeight = () => {
            if (containerRef.current) {
                const rect = containerRef.current.getBoundingClientRect();
                const availableHeight = window.innerHeight - rect.top - 20;
                setContainerHeight(Math.max(300, availableHeight));
            }
        };

        updateHeight();
        window.addEventListener('resize', updateHeight);

        return () => {
            window.removeEventListener('resize', updateHeight);
        };
    }, [containerRef]);

    return containerHeight;
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
 * Convert bytes array to base64 string (required for gRPC-gateway)
 */
export const bytesToBase64 = (bytes: number[]): string => {
    const binary = String.fromCharCode(...bytes);
    return btoa(binary);
};

/**
 * Parse comma-separated CIDR prefixes to IPNet array with base64-encoded bytes
 */
export const parsePrefixesToIPNets = (input: string): IPNet[] => {
    if (!input.trim()) return [];
    
    const parseIPToBytes = (ipStr: string): number[] | undefined => {
        if (ipStr.includes(':')) {
            // IPv6
            return parseIPv6ToBytes(ipStr);
        }
        return parseIPv4ToBytes(ipStr);
    };
    
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

/**
 * Parse IPv4 string to bytes array
 */
const parseIPv4ToBytes = (ipStr: string): number[] | undefined => {
    const parts = ipStr.split('.');
    if (parts.length !== 4) return undefined;
    
    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part, 10);
        if (isNaN(num) || num < 0 || num > 255) return undefined;
        bytes.push(num);
    }
    return bytes;
};

/**
 * Parse IPv6 string to bytes array
 */
const parseIPv6ToBytes = (ipStr: string): number[] | undefined => {
    const trimmed = ipStr.trim();
    if (!trimmed) return undefined;

    // Handle :: expansion
    let fullAddr = trimmed;
    if (trimmed.includes('::')) {
        const parts = trimmed.split('::');
        const left = parts[0] ? parts[0].split(':') : [];
        const right = parts[1] ? parts[1].split(':') : [];
        const missing = 8 - left.length - right.length;
        if (missing < 0) return undefined;
        const middle = Array(missing).fill('0');
        fullAddr = [...left, ...middle, ...right].join(':');
    }

    const parts = fullAddr.split(':');
    if (parts.length !== 8) return undefined;

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part || '0', 16);
        if (isNaN(num) || num < 0 || num > 0xffff) return undefined;
        bytes.push((num >> 8) & 0xff);
        bytes.push(num & 0xff);
    }
    return bytes;
};

/**
 * Create mask bytes from prefix length
 */
const prefixLengthToMaskBytes = (prefixLen: number, totalBytes: number): number[] => {
    const mask: number[] = [];
    let remaining = prefixLen;
    for (let i = 0; i < totalBytes; i++) {
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
    return mask;
};
