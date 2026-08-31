import React, { useMemo } from 'react';
import type { Rule } from '@yanet/core/api/forward';
import { ForwardMode, declaredForwardMode } from '@yanet/core/api/forward';
import { dumpYamlDoc } from '@yanet/core/utils';
import { SaveDiffModal as SharedSaveDiffModal } from '@yanet/core/components';
import { effectiveCounterName } from './hooks';

/**
 * Formats a mode as its declared name, an undeclared number as is, so one
 * rule from a newer module cannot hide the rest of a configuration.
 *
 * An absent mode is the zero value NONE, which the gateway omits from its
 * JSON.
 */
const formatForwardMode = (mode: ForwardMode | number | undefined): ForwardMode | number => {
    return declaredForwardMode(mode) ?? (typeof mode === 'number' ? mode : ForwardMode.NONE);
};

/**
 * Serialize a rules array into the wire request YAML: the document the
 * generic operator pushes and `yanet-cli-forward update` accepts.
 *
 * The `counter` key mirrors the raw stored value by default. Pass
 * `showEffectiveCounter` to emit the name the server would use instead
 * (`to_<target>` when unset), as the pre-save diff preview does.
 */
export const rulesToDiffYaml = (rules: Rule[], showEffectiveCounter = false): string => {
    const yamlRules = rules.map((r) => {
        const target = r.action?.target ?? '';
        const action: Record<string, unknown> = {
            target,
            mode: formatForwardMode(r.action?.mode),
        };
        if (showEffectiveCounter) {
            action['counter'] = effectiveCounterName(r.action?.counter ?? '', target);
        } else if (r.action?.counter) {
            action['counter'] = r.action.counter;
        }

        const entry: Record<string, unknown> = { action };
        const devices = (r.devices ?? []).map(d => ({ name: d.name ?? '' }));
        if (devices.length > 0) {
            entry['devices'] = devices;
        }
        const vlan_ranges = (r.vlan_ranges ?? []).map(vr => ({
            from: vr.from ?? 0,
            to: vr.to ?? 0,
        }));
        if (vlan_ranges.length > 0) {
            entry['vlan_ranges'] = vlan_ranges;
        }
        const networkLists = [
            ['sources4', r.sources4],
            ['sources6', r.sources6],
            ['destinations4', r.destinations4],
            ['destinations6', r.destinations6],
        ] as const;
        for (const [key, list] of networkLists) {
            if (list && list.length > 0) {
                entry[key] = list;
            }
        }

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
