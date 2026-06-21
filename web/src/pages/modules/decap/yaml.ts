import { dumpYamlDoc, parseYamlList } from '@yanet/core/utils';
import type { PrefixRowItem } from './types';

interface DecapYamlDoc {
    config: string;
    prefixes: string[];
}

/** Serialize prefix rows for the active config to YAML. */
export const rowsToYaml = (configName: string, rows: PrefixRowItem[]): string => {
    const doc: DecapYamlDoc = {
        config: configName,
        prefixes: rows.map((r) => r.prefix),
    };
    return dumpYamlDoc(doc);
};

/** Serialize prefix rows (without config wrapper) to YAML for diff display. */
export const rowsToDiffYaml = (rows: PrefixRowItem[]): string => {
    const doc = { prefixes: rows.map((r) => r.prefix) };
    return dumpYamlDoc(doc);
};

/**
 * Parse YAML (either { config, prefixes } or just { prefixes }) into prefix rows.
 * Returns the parsed rows. Throws with a descriptive message on failure.
 */
export const yamlToRows = (text: string): PrefixRowItem[] =>
    parseYamlList<PrefixRowItem>(text, 'prefixes', (p) => ({
        prefix: typeof p === 'string' ? p : '',
    }));
