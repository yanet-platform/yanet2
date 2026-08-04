import React, { useMemo } from 'react';
import type { Rule } from '@yanet/core/api/forward';
import { ForwardMode, FORWARD_MODE_LABELS } from '@yanet/core/api/forward';
import { formatIPNetItem, dumpYamlDoc } from '@yanet/core/utils';
import { SaveDiffModal as SharedSaveDiffModal } from '@yanet/core/components';
import { effectiveCounterName } from './hooks';

/**
 * Serialize a rules array into the canonical YAML schema.
 *
 * The `counter` key mirrors the raw stored value by default. Pass
 * `showEffectiveCounter` to emit the name the server would use instead
 * (`to_<target>` when unset), as the pre-save diff preview does.
 */
export const rulesToDiffYaml = (rules: Rule[], showEffectiveCounter = false): string => {
    const yamlRules = rules.map((r) => {
        const devices = (r.devices ?? []).map(d => d.name ?? '').filter(Boolean);
        const srcs = (r.srcs ?? []).map(formatIPNetItem).filter(Boolean);
        const dsts = (r.dsts ?? []).map(formatIPNetItem).filter(Boolean);
        const vlan_ranges = (r.vlan_ranges ?? []).map(vr => ({
            from: vr.from ?? 0,
            to: vr.to ?? 0,
        }));

        const target = r.action?.target ?? '';
        const entry: Record<string, unknown> = {
            target,
        };
        if (showEffectiveCounter) {
            entry['counter'] = effectiveCounterName(r.action?.counter ?? '', target);
        } else if (r.action?.counter) {
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
        entry['mode'] = FORWARD_MODE_LABELS[r.action?.mode ?? ForwardMode.NONE] ?? 'NONE';

        return entry;
    });

    return dumpYamlDoc({ rules: yamlRules });
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
    const beforeYaml = useMemo(() => rulesToDiffYaml(serverRules, true), [serverRules]);
    const afterYaml = useMemo(() => rulesToDiffYaml(draftRules, true), [draftRules]);

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
