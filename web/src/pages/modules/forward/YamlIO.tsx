import React, { useState } from 'react';
import yaml from 'js-yaml';
import type { Rule } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';
import { toaster } from '../../../utils';
import { parseCidrsToIPNets } from './hooks';
import { rulesToDiffYaml } from './SaveDiffModal';
import YamlIOModal from '../../../components/YamlIOModal';

/** Raw shape of a rule entry in the new YAML schema. */
interface YamlVlanRange {
    from: number;
    to: number;
}

interface YamlRule {
    target: string;
    counter?: string;
    vlan_ranges?: YamlVlanRange[];
    srcs?: string[] | null;
    dsts?: string[] | null;
    devices?: string[] | null;
    mode?: string;
}

/** Parse a YAML string into rules using the canonical schema.
 *
 * Top-level key is `rules`. Config name comes from outside the YAML (the import UI).
 * Returns the parsed rules array on success, throws with a descriptive message on failure.
 */
export const parseYamlToRules = (text: string): Rule[] => {
    let parsed: unknown;
    try {
        parsed = yaml.load(text);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (!parsed || typeof parsed !== 'object') {
        throw new Error('Expected a YAML object with a "rules" list.');
    }

    const doc = parsed as Record<string, unknown>;

    if (!Array.isArray(doc['rules'])) {
        throw new Error('Expected a top-level "rules" list.');
    }

    const modeMap: Record<string, ForwardMode> = {
        IN: ForwardMode.IN,
        OUT: ForwardMode.OUT,
        NONE: ForwardMode.NONE,
        In: ForwardMode.IN,
        Out: ForwardMode.OUT,
        None: ForwardMode.NONE,
    };

    const rules: Rule[] = (doc['rules'] as unknown[]).map((r: unknown): Rule => {
        if (!r || typeof r !== 'object') {
            return { action: { target: '', mode: ForwardMode.NONE } };
        }
        const row = r as YamlRule;

        const target = typeof row.target === 'string' ? row.target : '';
        const counter = typeof row.counter === 'string' ? row.counter : undefined;
        const modeRaw = typeof row.mode === 'string' ? row.mode : 'None';
        const mode = modeMap[modeRaw] ?? ForwardMode.NONE;

        const devicesRaw = Array.isArray(row.devices) ? row.devices : [];
        const devices = (devicesRaw as unknown[])
            .filter((d): d is string => typeof d === 'string')
            .map(name => ({ name }));

        const vlanRangesRaw = Array.isArray(row.vlan_ranges) ? row.vlan_ranges : [];
        const vlan_ranges = (vlanRangesRaw as unknown[]).map((vr: unknown) => {
            if (!vr || typeof vr !== 'object') return { from: 0, to: 0 };
            const v = vr as Record<string, unknown>;
            return {
                from: typeof v['from'] === 'number' ? v['from'] : 0,
                to: typeof v['to'] === 'number' ? v['to'] : 0,
            };
        });

        const srcsRaw = Array.isArray(row.srcs) ? row.srcs : [];
        const srcs = parseCidrsToIPNets(
            (srcsRaw as unknown[]).filter((s): s is string => typeof s === 'string'),
        );

        const dstsRaw = Array.isArray(row.dsts) ? row.dsts : [];
        const dsts = parseCidrsToIPNets(
            (dstsRaw as unknown[]).filter((s): s is string => typeof s === 'string'),
        );

        return { action: { target, mode, counter }, devices, vlan_ranges, srcs, dsts };
    });

    return rules;
};

interface YamlIOProps {
    configName: string;
    /** Draft rules for the current config (used for export). */
    rules: Rule[];
    /** Called when user imports rules into a config. Receives the target config name and parsed rules. */
    onImport: (configName: string, rules: Rule[]) => void;
}

/** YAML import/export controls rendered inline in the page header. */
const YamlIO: React.FC<YamlIOProps> = ({ configName, rules, onImport }) => {
    const [importConfigName, setImportConfigName] = useState(configName);

    const handleImport = (text: string): void => {
        const parsed = parseYamlToRules(text);
        const targetConfig = importConfigName.trim() || configName;
        onImport(targetConfig, parsed);
        toaster.success('fw-yaml-import', `Imported ${parsed.length} rules into "${targetConfig}".`);
    };

    const importExtraControls = (
        <div className="fw-field" style={{ marginBottom: 0, minWidth: 200 }}>
            <label className="fw-field__label" htmlFor="fw-import-config-name">
                Config name
            </label>
            <input
                id="fw-import-config-name"
                className="fw-input"
                type="text"
                value={importConfigName}
                onChange={(e) => setImportConfigName(e.target.value)}
                placeholder={configName}
            />
            <span className="fw-field__hint">
                Rules will be imported into this config (creates it locally if new).
            </span>
        </div>
    );

    return (
        <YamlIOModal
            configName={configName}
            itemCount={rules.length}
            itemLabel="rules"
            exportYaml={() => rulesToDiffYaml(rules)}
            onImport={handleImport}
            toastPrefix="fw-yaml"
            importPlaceholder={'rules:\n  - target: eth0\n    mode: Out\n    srcs:\n      - 10.0.0.0/8'}
            exportFooterHint="Exports current draft rules (unsaved changes included)."
            importFooterHint="Importing replaces all rules in the target config locally. Use Save to push to the server."
            importButtonLabel="Import"
            importExtraControls={importExtraControls}
        />
    );
};

export default YamlIO;
