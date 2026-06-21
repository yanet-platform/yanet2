import type { Rule, VlanRange } from '@yanet/core/api/forward';
import { ForwardMode } from '@yanet/core/api/forward';
import { formatIPNetItem, formatRange, parseCidrsToIPNets, parseRangesRaw } from '@yanet/core/utils';
import type { RuleItem, RuleDraft } from './types';

/** Format VlanRange array to a display string. */
const formatVlanRanges = (ranges: VlanRange[] | undefined): string => {
    if (!ranges || ranges.length === 0) return '';
    return ranges.map((r) => formatRange(r)).join(', ');
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

/** Convert a RuleDraft to a Rule for the API. */
export const draftToRule = (draft: RuleDraft): Rule => ({
    action: {
        target: draft.target,
        mode: draft.mode,
        counter: draft.counter || undefined,
    },
    devices: draft.deviceNames.map((name) => ({ name })),
    vlan_ranges: parseRangesRaw(draft.vlansRaw),
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
