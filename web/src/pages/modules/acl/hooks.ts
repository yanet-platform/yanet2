import { useEffect } from 'react';
import type { Rule, PortRange, VlanRange, ProtoRange, Action } from '../../../api/acl';
import { ActionKind } from '../../../api/acl';
import { formatIPNet } from '../../../utils';
import { extractBytes } from './utils';
import type { RuleDraft, RuleItem } from './types';
import { parseCidrsToIPNets, parseRangesRaw, parseProtoRangesRaw } from './parseHelpers';
export { parseCidrsToIPNets, parseRangesRaw, parseProtoRangesRaw } from './parseHelpers';

/**
 * Normalize a wire-shape Action.kind into a concrete ActionKind.
 *
 * proto3 omits default-value (0) fields from JSON, so a PASS action
 * arrives as `{}` with kind=undefined. Treat undefined as PASS, not
 * a missing action.
 */
export const normalizeActionKind = (kind: ActionKind | undefined): ActionKind =>
    kind ?? ActionKind.ACTION_KIND_PASS;

/** Format a single IPNet (base64 bytes) to a CIDR string. */
const formatIPNetItem = (net: { addr?: string | Uint8Array | number[]; mask?: string | Uint8Array | number[] }): string => {
    const addrBytes = extractBytes(net.addr);
    const maskBytes = extractBytes(net.mask);
    if (!addrBytes || addrBytes.length === 0) return '';
    return formatIPNet(addrBytes, maskBytes);
};

/** Format a PortRange or ProtoRange {from, to} to a string like "80" or "80-90". */
const formatRange = (r: { from?: number; to?: number }): string => {
    const from = r.from ?? 0;
    const to = r.to ?? 0;
    if (from === to) return String(from);
    return `${from}-${to}`;
};

/** Returns true when port ranges cover the full 0-65535 domain. */
const coversAllPorts = (ranges: PortRange[]): boolean => {
    if (ranges.length === 0) return true;
    return ranges.some(r => (r.from ?? 0) === 0 && (r.to ?? 0) >= 65535);
};

/** Returns true when proto ranges cover the full 0-65535 encoded domain. */
const coversAllProtos = (ranges: ProtoRange[]): boolean => {
    if (ranges.length === 0) return false;
    return ranges.some(r => (r.from ?? 0) === 0 && (r.to ?? 0) >= 65535);
};

/** Returns true when VLAN ranges cover the full 0-4095 domain. */
const coversAllVlans = (ranges: VlanRange[]): boolean => {
    if (ranges.length === 0) return true;
    return ranges.some(r => (r.from ?? 0) === 0 && (r.to ?? 0) >= 4095);
};

/**
 * Expand a Rule into its fully-formatted display shape.
 *
 * This is the expensive part: it decodes base64 CIDRs and formats all the
 * range arrays into human-readable strings. Call this inside a per-row
 * useMemo or on drawer open so only visible rows pay the cost.
 *
 * CIDRs are rendered verbatim — no sentinel collapsing. The classification
 * booleans (isL2, isDead, isDeadIp, isDeadProto) reflect the dataplane
 * semantics from modules/acl/api/controlplane.c:235-289:
 * - isL2: no IP entries on either side — rule fires on L2 frames only.
 * - isDeadIp: asymmetric src/dst IP families — no family matched on both sides.
 * - isDeadProto: IP rule with an empty proto_ranges array — matches no traffic.
 * - isDead: isDeadIp || isDeadProto.
 * - isEmptySrc: sourceCidrs.length === 0 (no IP match on sources side).
 */
export const expandRule = (rule: Rule): {
    sourceCidrs: string[];
    isEmptySrc: boolean;
    dstCidrs: string[];
    srcPortRanges: string[];
    isAnySrcPort: boolean;
    dstPortRanges: string[];
    isAnyDstPort: boolean;
    protoRanges: string[];
    isAnyProto: boolean;
    vlanRanges: string[];
    isAnyVlan: boolean;
    deviceNames: string[];
    isL2: boolean;
    isDeadIp: boolean;
    isDeadProto: boolean;
    isDead: boolean;
} => {
    const srcs = rule.srcs ?? [];
    const dsts = rule.dsts ?? [];

    const sourceCidrs = srcs.map(formatIPNetItem).filter(Boolean);
    const dstCidrs = dsts.map(formatIPNetItem).filter(Boolean);

    const srcPortRanges = (rule.src_port_ranges ?? []).map(formatRange);
    const dstPortRanges = (rule.dst_port_ranges ?? []).map(formatRange);
    const protoRanges = (rule.proto_ranges ?? []).map(formatRange);
    const vlanRanges = (rule.vlan_ranges ?? []).map(formatRange);
    const deviceNames = (rule.devices ?? []).map(d => d.name ?? '').filter(Boolean);

    // Per-family counts for classification (addr byte length: 4 = IPv4, 16 = IPv6).
    let v4SrcCount = 0;
    let v6SrcCount = 0;
    for (const net of srcs) {
        const len = extractBytes(net.addr)?.length ?? 0;
        if (len === 4) v4SrcCount++;
        else if (len === 16) v6SrcCount++;
    }

    let v4DstCount = 0;
    let v6DstCount = 0;
    for (const net of dsts) {
        const len = extractBytes(net.addr)?.length ?? 0;
        if (len === 4) v4DstCount++;
        else if (len === 16) v6DstCount++;
    }

    const hasIP4 = v4SrcCount > 0 && v4DstCount > 0;
    const hasIP6 = v6SrcCount > 0 && v6DstCount > 0;
    const isL2 = v4SrcCount === 0 && v4DstCount === 0 && v6SrcCount === 0 && v6DstCount === 0;
    // isDeadIp: asymmetric src/dst IP families — no single family is matched on both sides.
    const isDeadIp = !hasIP4 && !hasIP6 && !isL2;
    // isDeadProto: an IP rule with an empty proto_ranges array matches no traffic. The proto
    // compiler lacks the "count === 0 → full range" fallback that ports/vlans/devices have.
    const isDeadProto = !isL2 && (rule.proto_ranges?.length ?? 0) === 0;
    const isDead = isDeadIp || isDeadProto;

    return {
        sourceCidrs,
        isEmptySrc: sourceCidrs.length === 0,
        dstCidrs,
        srcPortRanges,
        isAnySrcPort: coversAllPorts(rule.src_port_ranges ?? []),
        dstPortRanges,
        isAnyDstPort: coversAllPorts(rule.dst_port_ranges ?? []),
        protoRanges,
        isAnyProto: coversAllProtos(rule.proto_ranges ?? []),
        vlanRanges,
        isAnyVlan: coversAllVlans(rule.vlan_ranges ?? []),
        deviceNames,
        isL2,
        isDeadIp,
        isDeadProto,
        isDead,
    };
};

/**
 * Return a human-readable tooltip string explaining why a rule is dead.
 *
 * Both isDeadIp and isDeadProto may be true simultaneously; when they are,
 * a combined message is returned. Returns an empty string if neither is true.
 */
export const deadReasonText = (expanded: { isDeadIp: boolean; isDeadProto: boolean }): string => {
    const { isDeadIp, isDeadProto } = expanded;
    if (isDeadIp && isDeadProto) {
        return "IP sources/destinations don't match and protocol filter is empty — rule matches no packets";
    }
    if (isDeadIp) {
        return "IP sources/destinations don't match — rule matches no packets";
    }
    if (isDeadProto) {
        return 'Protocol filter is empty — rule matches no packets';
    }
    return '';
};

/**
 * Alias for expandRule — retained for call sites that use the longer name.
 *
 * Expands a wire-format Rule into its fully-decoded display shape.
 */
export const expandRuleItem = expandRule;

/**
 * Return the default counter name the dataplane assigns to a rule at position idx.
 *
 * The synthetic name is derived from the rule's CURRENT draft index. The dataplane
 * registers counters using the LAST COMMITTED index, so if the draft has reordered
 * rules, sparkline values may temporarily disconnect until the draft is committed.
 * This is accepted behaviour; no UI warning is shown.
 */
export const defaultCounterName = (idx: number): string => `rule ${idx}`;

/**
 * Return the counter name that the dataplane will actually use for this rule.
 *
 * If the rule has an explicit counter set, that name is returned verbatim.
 * Otherwise the synthetic default name derived from the rule's index is returned.
 */
export const effectiveCounterName = (rule: Rule, index: number): string =>
    rule.counter || defaultCounterName(index);

/** Convert a Rule array and stable ID array to RuleItem array for UI display. */
export const rulesToNgItems = (rules: Rule[], ids: string[]): RuleItem[] =>
    rules.map((rule, index) => {
        const expanded = expandRule(rule);
        const counter = rule.counter ?? '';
        const searchText = [
            counter,
            defaultCounterName(index),
            ...expanded.sourceCidrs,
            ...expanded.dstCidrs,
            ...expanded.deviceNames,
        ].join('\n').toLowerCase();
        return {
            id: ids[index] ?? `rule-${index}`,
            index,
            rule,
            counter,
            searchText,
        };
    });

/** Convert a RuleItem back to a RuleDraft for drawer editing. */
export const itemToDraft = (item: RuleItem): RuleDraft => ruleToDraft(item.rule);

/** Convert a ProtoRange wire object to the encoded-range string "A-B". */
export const protoRangeToStr = (r: ProtoRange): string => {
    const from = r.from ?? 0;
    const to = r.to ?? 0;
    if (from === to) return String(from);
    return `${from}-${to}`;
};

/** Convert a RuleDraft to a wire Rule. */
export const draftToRule = (draft: RuleDraft): Rule => {
    const actions: Action[] = draft.actions.map(kind => ({ kind }));
    return {
        actions,
        counter: draft.counter || undefined,
        devices: draft.deviceNames.map(name => ({ name })),
        vlan_ranges: parseRangesRaw(draft.vlanRaw),
        srcs: parseCidrsToIPNets(draft.sourceCidrs),
        dsts: parseCidrsToIPNets(draft.dstCidrs),
        proto_ranges: parseProtoRangesRaw(draft.protoRaw),
        src_port_ranges: parseRangesRaw(draft.srcPortRaw),
        dst_port_ranges: parseRangesRaw(draft.dstPortRaw),
    };
};

/** Convert a Rule to a RuleDraft for drawer editing. */
export const ruleToDraft = (rule: Rule): RuleDraft => {
    const expanded = expandRule(rule);
    return {
        sourceCidrs: [...expanded.sourceCidrs],
        dstCidrs: [...expanded.dstCidrs],
        srcPortRaw: expanded.srcPortRanges.join(', '),
        dstPortRaw: expanded.dstPortRanges.join(', '),
        protoRaw: (rule.proto_ranges ?? []).map(protoRangeToStr).join(', '),
        vlanRaw: expanded.vlanRanges.join(', '),
        deviceNames: [...expanded.deviceNames],
        counter: rule.counter ?? '',
        actions: (rule.actions ?? []).map(a => normalizeActionKind(a.kind)),
    };
};

/** Validate CIDR string (IPv4 or IPv6). Returns true if valid. */
export const isValidCidr = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed) return false;
    const ipv4 = trimmed.match(/^(\d{1,3}(?:\.\d{1,3}){3})(?:\/(\d{1,2}))?$/);
    if (ipv4) {
        const parts = ipv4[1].split('.').map(Number);
        if (parts.some(n => n > 255)) return false;
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

/** Keyboard shortcuts for the ACL page. */
export const useKeyboardShortcuts = (opts: {
    onNewRule: () => void;
    onFocusSearch: () => void;
    onEscape: () => void;
    drawerOpen: boolean;
}): void => {
    const { onNewRule, onFocusSearch, onEscape, drawerOpen } = opts;

    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape' && drawerOpen) {
                onEscape();
                return;
            }
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
            if (e.key === '/' && !e.metaKey && !e.ctrlKey) {
                e.preventDefault();
                onFocusSearch();
            } else if (e.key === 'n' && !e.metaKey && !e.ctrlKey && !e.altKey) {
                onNewRule();
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [onNewRule, onFocusSearch, onEscape, drawerOpen]);
};
