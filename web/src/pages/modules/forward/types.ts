import type { Rule } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';

export type { Rule };
export { ForwardMode };

/** A flat, UI-friendly representation of a forward rule for table and drawer use. */
export interface RuleItem {
    id: string;
    index: number;
    rule: Rule;
    /** Parsed target string. */
    target: string;
    /** Parsed mode. */
    mode: ForwardMode;
    /** Parsed counter name. */
    counter: string;
    /** Parsed device names. */
    deviceNames: string[];
    /** Formatted VLAN string for display (e.g. "0-4095", "100-200"). */
    vlansDisplay: string;
    /** Whether VLANs cover full 0-4095 range (triggers ALL VLANs badge). */
    isAllVlans: boolean;
    /** Formatted source CIDR strings. */
    sourceCidrs: string[];
    /** Whether sources is wildcard (*). */
    isAnySrc: boolean;
    /** Formatted destination CIDR strings. */
    dstCidrs: string[];
    /** Whether destinations is wildcard (*). */
    isAnyDst: boolean;
}

/** Draft state used inside RuleDrawer form. */
export interface RuleDraft {
    target: string;
    mode: ForwardMode;
    counter: string;
    deviceNames: string[];
    vlansRaw: string;
    sourceCidrs: string[];
    dstCidrs: string[];
}

export const emptyDraft = (): RuleDraft => ({
    target: '',
    mode: ForwardMode.NONE,
    counter: '',
    deviceNames: [],
    vlansRaw: '',
    sourceCidrs: [],
    dstCidrs: [],
});

/** Mode label strings for display. */
export const MODE_LABELS: Record<ForwardMode, string> = {
    [ForwardMode.NONE]: 'NONE',
    [ForwardMode.IN]: 'IN',
    [ForwardMode.OUT]: 'OUT',
};
