import React, { useState } from 'react';
import { Dialog, Text } from '@gravity-ui/uikit';
import * as yaml from 'js-yaml';
import type { Rule } from '../../../api/acl-ng';
import { ActionKind } from '../../../api/acl-ng';
import { formatIPNet } from '../../../utils';
import { extractBytes } from './utils';

// TODO(acl-ng): structured diff disabled until the per-card layout is reworked.

const ACTION_KIND_YAML_NAMES: Record<ActionKind, string> = {
    [ActionKind.ACTION_KIND_PASS]: 'ACTION_KIND_PASS',
    [ActionKind.ACTION_KIND_DENY]: 'ACTION_KIND_DENY',
    [ActionKind.ACTION_KIND_COUNT]: 'ACTION_KIND_COUNT',
    [ActionKind.ACTION_KIND_CHECK_STATE]: 'ACTION_KIND_CHECK_STATE',
    [ActionKind.ACTION_KIND_CREATE_STATE]: 'ACTION_KIND_CREATE_STATE',
    [ActionKind.ACTION_KIND_LOG]: 'ACTION_KIND_LOG',
};

/** Build the serialisable object array for a set of ACL rules. Used by both YAML and JSON export. */
export const rulesToYamlObjects = (rules: Rule[]): Array<Record<string, unknown>> => {
    return rules.map(r => {
        const fmtIPNet = (net: { addr?: string | Uint8Array | number[]; mask?: string | Uint8Array | number[] }): string => {
            const addrBytes = extractBytes(net.addr);
            const maskBytes = extractBytes(net.mask);
            if (!addrBytes) return '';
            return formatIPNet(addrBytes, maskBytes);
        };
        const srcs = (r.srcs ?? []).map(fmtIPNet).filter(Boolean);
        const dsts = (r.dsts ?? []).map(fmtIPNet).filter(Boolean);
        const fmtRange = (rng: { from?: number; to?: number }): string => {
            const from = rng.from ?? 0;
            const to = rng.to ?? 0;
            if (from === to) return String(from);
            return `${from}-${to}`;
        };
        const src_port_ranges = (r.src_port_ranges ?? []).map(fmtRange);
        const dst_port_ranges = (r.dst_port_ranges ?? []).map(fmtRange);
        const proto_ranges = (r.proto_ranges ?? []).map(fmtRange);
        const vlan_ranges = (r.vlan_ranges ?? []).map(fmtRange);
        const devices = (r.devices ?? []).map(d => d.name ?? '').filter(Boolean);
        const actions = (r.actions ?? []).map(a => ({
            kind: ACTION_KIND_YAML_NAMES[a.kind ?? ActionKind.ACTION_KIND_PASS] ?? 'ACTION_KIND_PASS',
        }));

        const entry: Record<string, unknown> = {};
        if (srcs.length > 0) entry['srcs'] = srcs;
        if (dsts.length > 0) entry['dsts'] = dsts;
        if (src_port_ranges.length > 0) entry['src_port_ranges'] = src_port_ranges;
        if (dst_port_ranges.length > 0) entry['dst_port_ranges'] = dst_port_ranges;
        if (proto_ranges.length > 0) entry['proto_ranges'] = proto_ranges;
        if (vlan_ranges.length > 0) entry['vlan_ranges'] = vlan_ranges;
        if (devices.length > 0) entry['devices'] = devices;
        if (r.counter) entry['counter'] = r.counter;
        entry['actions'] = actions;
        return entry;
    });
};

/** Serialize ACL rules to the canonical YAML schema matching yanet-cli acl show output. */
export const rulesToDiffYaml = (rules: Rule[]): string =>
    yaml.dump(
        { rules: rulesToYamlObjects(rules) },
        { sortKeys: false, lineWidth: 120, noRefs: true },
    );

interface SaveDiffModalProps {
    configName: string;
    draftRules: Rule[];
    draftIds: string[];
    serverRules: Rule[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/** Confirmation modal for saving the current ACL NG draft. */
export const SaveDiffModal: React.FC<SaveDiffModalProps> = ({
    configName,
    onClose,
    onApply,
}) => {
    const [applying, setApplying] = useState(false);

    const handleApply = async (): Promise<void> => {
        setApplying(true);
        try {
            await onApply();
        } finally {
            setApplying(false);
        }
    };

    return (
        <Dialog open onClose={onClose} size="s" disableOutsideClick={applying} disableEscapeKeyDown={applying}>
            <Dialog.Header caption="Save changes" />
            <Dialog.Body>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <Text variant="subheader-2">Diff preview is under construction.</Text>
                    <Text variant="body-2" color="secondary">
                        Click &ldquo;Save&rdquo; to push the current draft of{' '}
                        <Text variant="code-inline-2">{configName}</Text>{' '}
                        to the server.
                    </Text>
                </div>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonApply="Save"
                textButtonCancel="Cancel"
                loading={applying}
            />
        </Dialog>
    );
};
