import React, { useState } from 'react';
import yaml from 'js-yaml';
import type { Rule } from '@yanet/core/api/forward';
import { declaredForwardMode } from '@yanet/core/api/forward';
import { toaster } from '@yanet/core/utils';
import { isValidIPv4Address, isValidIPv6Address } from '@yanet/core/utils/netip';
import { rulesToDiffYaml } from './SaveDiffModal';
import YamlIOModal from '@yanet/core/components/YamlIOModal';

/** A parsed wire update document: the optional config name and the rules. */
export interface ParsedRulesDoc {
    name?: string;
    rules: Rule[];
}

/**
 * Refuses a mapping key outside the known set, naming the legacy schema
 * when the key belongs to it, so an old flat-format file fails loudly
 * instead of importing empty rules.
 */
const checkKnownKeys = (value: Record<string, unknown>, where: string, known: string[]): void => {
    const legacy = ['target', 'mode', 'counter', 'srcs', 'dsts'];
    for (const key of Object.keys(value)) {
        if (known.includes(key)) {
            continue;
        }
        if (legacy.includes(key)) {
            throw new Error(
                `Unknown key "${key}" in ${where}: this is the retired flat schema. ` +
                'Export the wire form with "yanet-cli-forward show" or the page Export.',
            );
        }
        throw new Error(`Unknown key "${key}" in ${where}, expected ${known.map(k => `"${k}"`).join(', ')}.`);
    }
};

/**
 * Tells a plain YAML mapping from every other loaded value, including the
 * Date a bare timestamp scalar becomes and the array a sequence becomes.
 */
const isPlainMapping = (value: unknown): value is Record<string, unknown> => {
    if (value == null || typeof value !== 'object' || Array.isArray(value)) {
        return false;
    }
    const proto: unknown = Object.getPrototypeOf(value);
    return proto === Object.prototype || proto === null;
};

/** Reads a string field, a null as the empty string, other types refused. */
const stringField = (value: unknown, where: string): string => {
    if (value == null) {
        return '';
    }
    if (typeof value !== 'string') {
        throw new Error(`Expected ${where} to be a string.`);
    }
    return value;
};

/** Reads an integer field, a null as zero, any other spelling refused. */
const uint32Field = (value: unknown, where: string): number => {
    if (value == null) {
        return 0;
    }
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > 4294967295) {
        throw new Error(`Expected ${where} to be an unsigned integer.`);
    }
    return value;
};

/** Reads a string list field, a null as the empty list. */
const stringList = (value: unknown, where: string): string[] => {
    if (value == null) {
        return [];
    }
    if (!Array.isArray(value) || value.some(entry => typeof entry !== 'string')) {
        throw new Error(`Expected ${where} to be a list of strings.`);
    }
    return value as string[];
};

/**
 * Accepts a network in the forms the wire types parse: CIDR, explicit
 * address/mask — covering the bi-contiguous IPv6 networks the filter
 * compiler supports — or a bare host address.
 */
const isValidNetwork = (net: string, isAddress: (addr: string) => boolean, maxLength: number): boolean => {
    const slash = net.indexOf('/');
    if (slash < 0) {
        return isAddress(net);
    }
    const address = net.slice(0, slash);
    const suffix = net.slice(slash + 1);
    if (!isAddress(address)) {
        return false;
    }
    // The strict form refuses a padded length such as /024, which the
    // wire parser rejects too.
    if (/^(0|[1-9][0-9]*)$/.test(suffix)) {
        const length = Number(suffix);
        return length <= maxLength;
    }
    return isAddress(suffix);
};

const isValidIPv4Network = (net: string): boolean => isValidNetwork(net, isValidIPv4Address, 32);
const isValidIPv6Network = (net: string): boolean => isValidNetwork(net, isValidIPv6Address, 128);

/** Reads a family-typed network list, refusing an entry of the wrong family. */
const networkList = (value: unknown, where: string, isValid: (net: string) => boolean): string[] => {
    const nets = stringList(value, where);
    for (const net of nets) {
        if (!isValid(net)) {
            throw new Error(`${where} entry "${net}" is not a valid network for that family.`);
        }
    }
    return nets;
};

/**
 * Parse a YAML string as the wire update request: the document the
 * generic operator pushes and `yanet-cli-forward` prints and accepts.
 *
 * A null field reads as its zero value and an unknown key is refused,
 * as the other readers of the format do. Throws with a descriptive
 * message on failure.
 */
export const parseYamlToRules = (text: string): ParsedRulesDoc => {
    let documents: unknown[];
    try {
        // The stream is read whole, so a bare trailing separator is
        // tolerated the way the operator and the CLI read it.
        documents = (yaml.loadAll(text) as unknown[]).filter(doc => doc != null);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (documents.length === 0) {
        return { rules: [] };
    }
    if (documents.length > 1) {
        throw new Error('The file holds more than one document.');
    }
    const parsed = documents[0];
    if (!isPlainMapping(parsed)) {
        throw new Error('Expected a YAML object with a "rules" list.');
    }

    const doc = parsed;
    checkKnownKeys(doc, 'the document', ['name', 'rules']);

    if (doc['name'] != null && typeof doc['name'] !== 'string') {
        throw new Error('Expected "name" to be a string.');
    }
    const name = typeof doc['name'] === 'string' && doc['name'] !== '' ? doc['name'] : undefined;
    if (doc['rules'] != null && !Array.isArray(doc['rules'])) {
        throw new Error('Expected a top-level "rules" list.');
    }
    const rows = (doc['rules'] ?? []) as unknown[];

    const rules: Rule[] = rows.map((row: unknown, idx: number): Rule => {
        if (!isPlainMapping(row)) {
            throw new Error(`Rule ${idx} is not a mapping.`);
        }
        const rule = row;
        checkKnownKeys(rule, `rule ${idx}`, [
            'action', 'devices', 'vlan_ranges', 'sources4', 'sources6', 'destinations4', 'destinations6',
        ]);

        const actionRaw = rule['action'];
        if (!isPlainMapping(actionRaw)) {
            throw new Error(`Rule ${idx}: "action" is required and must be a mapping.`);
        }
        const action = actionRaw;
        checkKnownKeys(action, `rule ${idx} action`, ['target', 'mode', 'counter']);

        const modeRaw = action['mode'];
        const mode = modeRaw == null
            ? declaredForwardMode(0)
            : declaredForwardMode(modeRaw as string | number);
        if (mode === undefined) {
            throw new Error(`Rule ${idx}: unknown forward mode ${JSON.stringify(modeRaw)}.`);
        }

        const devicesRaw = rule['devices'] == null ? [] : rule['devices'];
        if (!Array.isArray(devicesRaw)) {
            throw new Error(`Rule ${idx}: "devices" is not a list.`);
        }
        const devices = devicesRaw.map((d: unknown, deviceIdx: number) => {
            if (!isPlainMapping(d)) {
                throw new Error(`Rule ${idx}: device ${deviceIdx} is not a mapping with a "name".`);
            }
            checkKnownKeys(d, `rule ${idx} device ${deviceIdx}`, ['name']);
            return { name: stringField(d['name'], `rule ${idx} device ${deviceIdx} "name"`) };
        });

        const vlanRaw = rule['vlan_ranges'] == null ? [] : rule['vlan_ranges'];
        if (!Array.isArray(vlanRaw)) {
            throw new Error(`Rule ${idx}: "vlan_ranges" is not a list.`);
        }
        const vlan_ranges = vlanRaw.map((vr: unknown, rangeIdx: number) => {
            if (!isPlainMapping(vr)) {
                throw new Error(`Rule ${idx}: vlan range ${rangeIdx} is not a mapping.`);
            }
            checkKnownKeys(vr, `rule ${idx} vlan range ${rangeIdx}`, ['from', 'to']);
            return {
                from: uint32Field(vr['from'], `rule ${idx} vlan range ${rangeIdx} "from"`),
                to: uint32Field(vr['to'], `rule ${idx} vlan range ${rangeIdx} "to"`),
            };
        });

        const counter = stringField(action['counter'], `rule ${idx} action "counter"`);
        return {
            action: {
                target: stringField(action['target'], `rule ${idx} action "target"`),
                mode,
                counter: counter !== '' ? counter : undefined,
            },
            devices,
            vlan_ranges,
            sources4: networkList(rule['sources4'], `rule ${idx} sources4`, isValidIPv4Network),
            sources6: networkList(rule['sources6'], `rule ${idx} sources6`, isValidIPv6Network),
            destinations4: networkList(rule['destinations4'], `rule ${idx} destinations4`, isValidIPv4Network),
            destinations6: networkList(rule['destinations6'], `rule ${idx} destinations6`, isValidIPv6Network),
        };
    });

    return { name, rules };
};

interface YamlIOProps {
    configName: string;
    /** Draft rules for the current config (used for export). */
    rules: Rule[];
    /** Called when user imports rules into a config. Receives the target config name and parsed rules. */
    onImport: (configName: string, rules: Rule[]) => void;
    /** When true, the Import and Export trigger buttons are disabled. Default: false. */
    disabled?: boolean;
}

/** YAML import/export controls rendered inline in the page header. */
const YamlIO: React.FC<YamlIOProps> = ({ configName, rules, onImport, disabled }) => {
    const [importConfigName, setImportConfigName] = useState(configName);

    const handleImport = (text: string): void => {
        const parsed = parseYamlToRules(text);
        const targetConfig = importConfigName.trim() || parsed.name || configName;
        if (parsed.name && parsed.name !== targetConfig) {
            throw new Error(`The file names config "${parsed.name}", but "${targetConfig}" is chosen.`);
        }
        onImport(targetConfig, parsed.rules);
        toaster.success('yn-yaml-import', `Imported ${parsed.rules.length} rules into "${targetConfig}".`);
    };

    const importExtraControls = (
        <div className="yn-field" style={{ marginBottom: 0, minWidth: 200 }}>
            <label className="yn-field__label" htmlFor="yn-import-config-name">
                Config name
            </label>
            <input
                id="yn-import-config-name"
                className="yn-input"
                type="text"
                value={importConfigName}
                onChange={(e) => setImportConfigName(e.target.value)}
                placeholder={configName}
            />
            <span className="yn-field__hint">
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
            toastPrefix="yn-yaml"
            importPlaceholder={'rules:\n  - action:\n      target: eth0\n      mode: OUT\n    sources4:\n      - 10.0.0.0/8'}
            exportFooterHint="Exports current draft rules (unsaved changes included)."
            importFooterHint="Importing replaces all rules in the target config locally. Use Save to push to the server."
            importButtonLabel="Import"
            importExtraControls={importExtraControls}
            disabled={disabled}
        />
    );
};

export default YamlIO;
