import React, { useMemo } from 'react';
import * as yaml from 'js-yaml';
import type { Rule } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';
import { formatIPNet } from '../../../utils';
import { extractBytes } from './utils';
import { SaveDiffModal as SharedSaveDiffModal } from '../../../components';

/** Serialize a rules array into the canonical YAML schema for diff display. */
export const rulesToDiffYaml = (rules: Rule[]): string => {
    const yamlRules = rules.map((r) => {
        const modeMap: Record<number, string> = {
            [ForwardMode.NONE]: 'None',
            [ForwardMode.IN]: 'In',
            [ForwardMode.OUT]: 'Out',
        };

        const devices = (r.devices ?? []).map(d => d.name ?? '').filter(Boolean);
        const srcs = (r.srcs ?? []).map(net => {
            const addrBytes = extractBytes(net.addr);
            const maskBytes = extractBytes(net.mask);
            if (!addrBytes) return '';
            return formatIPNet(addrBytes, maskBytes);
        }).filter(Boolean);
        const dsts = (r.dsts ?? []).map(net => {
            const addrBytes = extractBytes(net.addr);
            const maskBytes = extractBytes(net.mask);
            if (!addrBytes) return '';
            return formatIPNet(addrBytes, maskBytes);
        }).filter(Boolean);
        const vlan_ranges = (r.vlan_ranges ?? []).map(vr => ({
            from: vr.from ?? 0,
            to: vr.to ?? 0,
        }));

        const entry: Record<string, unknown> = {
            target: r.action?.target ?? '',
        };
        if (r.action?.counter) {
            entry['counter'] = r.action.counter;
        }
        if (vlan_ranges.length > 0) {
            entry['vlan_ranges'] = vlan_ranges;
        }
        if (srcs.length > 0) {
            entry['srcs'] = srcs;
        }
        if (dsts.length > 0) {
            entry['dsts'] = dsts;
        }
        if (devices.length > 0) {
            entry['devices'] = devices;
        }
        entry['mode'] = modeMap[r.action?.mode ?? ForwardMode.NONE] ?? 'None';

        return entry;
    });

    return yaml.dump(
        { rules: yamlRules },
        { sortKeys: false, lineWidth: 120, noRefs: true },
    );
};

interface SaveDiffModalProps {
    configName: string;
    draftRules: Rule[];
    serverRules: Rule[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/**
 * Modal showing a side-by-side YAML diff of server vs draft rules for a config,
 * with an Apply button that calls onApply and closes on success.
 */
export const SaveDiffModal: React.FC<SaveDiffModalProps> = ({
    configName,
    draftRules,
    serverRules,
    onClose,
    onApply,
}) => {
    const beforeYaml = useMemo(() => rulesToDiffYaml(serverRules), [serverRules]);
    const afterYaml = useMemo(() => rulesToDiffYaml(draftRules), [draftRules]);

    return (
        <SharedSaveDiffModal
            configName={configName}
            beforeYaml={beforeYaml}
            afterYaml={afterYaml}
            applyLabel="Apply"
            onClose={onClose}
            onApply={onApply}
        />
    );
};
