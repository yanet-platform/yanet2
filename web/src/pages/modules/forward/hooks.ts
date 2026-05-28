import { useEffect } from 'react';
import type { Rule, IPNet, VlanRange } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';
import { formatIPNet, parseIPToBytes, prefixLengthToMaskBytes, bytesToBase64 } from '../../../utils';
import type { RuleItem, RuleDraft } from './types';
import { extractBytes } from './utils';

/** Format an IPNet to a CIDR string. */
const formatIPNetItem = (net: IPNet): string => {
    const addrBytes = extractBytes(net.addr);
    const maskBytes = extractBytes(net.mask);
    if (!addrBytes || addrBytes.length === 0) return '';
    return formatIPNet(addrBytes, maskBytes);
};

/** Format VlanRange array to a display string. */
const formatVlanRanges = (ranges: VlanRange[] | undefined): string => {
    if (!ranges || ranges.length === 0) return '';
    return ranges.map((r) => {
        const from = r.from ?? 0;
        const to = r.to ?? 0;
        if (from === to) return String(from);
        return `${from}-${to}`;
    }).join(', ');
};

/** Returns true if the VLAN ranges represent the full 0-4095 range or no restriction. */
const computeIsAllVlans = (ranges: VlanRange[] | undefined): boolean => {
    if (!ranges || ranges.length === 0) return true;
    if (ranges.length === 1) {
        const r = ranges[0];
        return (r.from ?? 0) === 0 && (r.to ?? 0) === 4095;
    }
    return false;
};

/** Convert a Rule array from the API to RuleItem array for UI display. */
export const rulesToNgItems = (rules: Rule[]): RuleItem[] => {
    return rules.map((rule, index) => {
        const deviceNames = (rule.devices || []).map((d) => d.name || '').filter(Boolean);
        const vlansDisplay = formatVlanRanges(rule.vlan_ranges);
        const isAllVlans = computeIsAllVlans(rule.vlan_ranges);
        const sourceCidrs = (rule.srcs || []).map(formatIPNetItem).filter(Boolean);
        const dstCidrs = (rule.dsts || []).map(formatIPNetItem).filter(Boolean);
        const mode = rule.action?.mode ?? ForwardMode.NONE;

        return {
            id: `ng-rule-${index}`,
            index,
            rule,
            target: rule.action?.target ?? '',
            mode,
            counter: rule.action?.counter ?? '',
            deviceNames,
            vlansDisplay,
            isAllVlans,
            sourceCidrs,
            isAnySrc: sourceCidrs.length === 0,
            dstCidrs,
            isAnyDst: dstCidrs.length === 0,
        };
    });
};

/** Parse a VLAN range string (e.g. "100-200, 300") to VlanRange array. */
export const parseVlanRangesStr = (input: string): VlanRange[] => {
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

/** Parse CIDR strings to IPNet array with base64-encoded bytes. */
export const parseCidrsToIPNets = (cidrs: string[]): IPNet[] => {
    const results: IPNet[] = [];
    for (const cidr of cidrs) {
        const parts = cidr.split('/');
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

/** Convert a RuleDraft to a Rule for the API. */
export const draftToRule = (draft: RuleDraft): Rule => ({
    action: {
        target: draft.target,
        mode: draft.mode,
        counter: draft.counter || undefined,
    },
    devices: draft.deviceNames.map((name) => ({ name })),
    vlan_ranges: parseVlanRangesStr(draft.vlansRaw),
    srcs: parseCidrsToIPNets(draft.sourceCidrs),
    dsts: parseCidrsToIPNets(draft.dstCidrs),
});

/** Convert a RuleItem back to a RuleDraft for editing. */
export const itemToDraft = (item: RuleItem): RuleDraft => ({
    target: item.target,
    mode: item.mode,
    counter: item.counter,
    deviceNames: [...item.deviceNames],
    vlansRaw: item.vlansDisplay,
    sourceCidrs: [...item.sourceCidrs],
    dstCidrs: [...item.dstCidrs],
});

/** Register keyboard shortcuts for the forward page. */
export const useKeyboardShortcuts = (opts: {
    onNewRule: () => void;
    onEscape: () => void;
    drawerOpen: boolean;
}): void => {
    const { onNewRule, onEscape, drawerOpen } = opts;

    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            // Escape always closes the drawer regardless of focus position.
            if (e.key === 'Escape' && drawerOpen) {
                onEscape();
                return;
            }
            // n is gated: do not fire when focus is inside a text field.
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA') return;
            if (e.key === 'n' && !e.metaKey && !e.ctrlKey && !e.altKey) {
                onNewRule();
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [onNewRule, onEscape, drawerOpen]);
};

/** Validate CIDR string (IPv4 or IPv6). Returns true if valid. */
export const isValidCidr = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed) return false;
    const ipv4 = trimmed.match(/^(\d{1,3}(?:\.\d{1,3}){3})(?:\/(\d{1,2}))?$/);
    if (ipv4) {
        const parts = ipv4[1].split('.').map(Number);
        if (parts.some((n) => n > 255)) return false;
        if (ipv4[2] !== undefined && (Number(ipv4[2]) < 0 || Number(ipv4[2]) > 32)) return false;
        return true;
    }
    const ipv6 = trimmed.match(/^([0-9a-fA-F:]+)(?:\/(\d{1,3}))?$/);
    if (ipv6 && trimmed.includes(':')) {
        if (ipv6[2] !== undefined && (Number(ipv6[2]) < 0 || Number(ipv6[2]) > 128)) return false;
        return true;
    }
    return false;
};

/** Validate device name string. */
export const isValidDeviceName = (s: string): boolean => /^[a-zA-Z0-9_:.\-]+$/.test(s.trim());

/** Validate VLAN token (single value or range, 0-4095). */
export const isValidVlanToken = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed) return false;
    const m = trimmed.match(/^(\d+)(?:-(\d+))?$/);
    if (!m) return false;
    const a = Number(m[1]);
    if (a < 0 || a > 4095) return false;
    if (m[2] !== undefined) {
        const b = Number(m[2]);
        if (b < 0 || b > 4095 || b < a) return false;
    }
    return true;
};
