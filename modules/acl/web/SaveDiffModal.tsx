import React, { useState } from 'react';
import { Dialog, Text } from '@gravity-ui/uikit';
import type { Rule } from '@yanet/core/api/acl';
import { ActionKind } from '@yanet/core/api/acl';
import { formatIPNetItem, dumpYamlDoc } from '@yanet/core/utils';

// TODO(acl): structured diff disabled until the per-card layout is reworked.

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
    const isV6 = (cidr: string): boolean => cidr.split('/')[0].includes(':');
    return rules.map(r => {
        const srcs = [...(r.srcs ?? []).map(formatIPNetItem).filter(Boolean), ...(r.sources4 ?? []), ...(r.sources6 ?? [])];
        const dsts = [
            ...(r.dsts ?? []).map(formatIPNetItem).filter(Boolean),
            ...(r.destinations4 ?? []),
            ...(r.destinations6 ?? []),
        ];
        const fmtRange = (rng: { from?: number; to?: number }): { from: number; to: number } => ({
            from: rng.from ?? 0,
            to: rng.to ?? 0,
        });
        const src_port_ranges = (r.src_port_ranges ?? []).map(fmtRange);
        const dst_port_ranges = (r.dst_port_ranges ?? []).map(fmtRange);
        const proto_ranges = (r.proto_ranges ?? []).map(fmtRange);
        const vlan_ranges = (r.vlan_ranges ?? []).map(fmtRange);
        const devices = (r.devices ?? []).map(d => ({ name: d.name ?? '' })).filter(d => d.name !== '');
        const actions = (r.actions ?? []).map(a => ({
            kind: ACTION_KIND_YAML_NAMES[a.kind ?? ActionKind.ACTION_KIND_PASS] ?? 'ACTION_KIND_PASS',
        }));

        const entry: Record<string, unknown> = {};
        const sources4 = srcs.filter(s => !isV6(s));
        const sources6 = srcs.filter(isV6);
        const destinations4 = dsts.filter(d => !isV6(d));
        const destinations6 = dsts.filter(isV6);
        if (sources4.length > 0) entry['sources4'] = sources4;
        if (sources6.length > 0) entry['sources6'] = sources6;
        if (destinations4.length > 0) entry['destinations4'] = destinations4;
        if (destinations6.length > 0) entry['destinations6'] = destinations6;
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
    dumpYamlDoc({ rules: rulesToYamlObjects(rules) });

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
