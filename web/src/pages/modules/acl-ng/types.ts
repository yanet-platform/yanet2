import type { Rule, ActionKind } from '../../../api/acl-ng';

export type { Rule, ActionKind };

/** Display item produced by rulesToNgItems — one per rule in the draft. */
export interface RuleItem {
    /** Stable unique ID (from the server-assigned rule ID). */
    id: string;
    /** Zero-based position in the rule array. */
    index: number;
    /** Raw wire-format rule. */
    rule: Rule;
    /** Counter name (empty string when unset). */
    counter: string;
}

/** Mutable draft state for the rule drawer form. */
export interface RuleDraft {
    sourceCidrs: string[];
    dstCidrs: string[];
    srcPortRaw: string;
    dstPortRaw: string;
    protoRaw: string;
    vlanRaw: string;
    deviceNames: string[];
    counter: string;
    actions: ActionKind[];
}

export const emptyDraft = (): RuleDraft => ({
    sourceCidrs: [],
    dstCidrs: [],
    srcPortRaw: '',
    dstPortRaw: '',
    protoRaw: '',
    vlanRaw: '',
    deviceNames: [],
    counter: '',
    actions: [],
});
