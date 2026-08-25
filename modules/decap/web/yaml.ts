import { dumpYamlDoc, parseYamlList } from '@yanet/core/utils';
import type { PrefixRowItem } from './types';

interface DecapYamlDoc {
    config: string;
    prefixes4: string[];
    prefixes6: string[];
}

/** Partition prefix rows into the family-typed lists the schema carries. */
const partitionRows = (rows: PrefixRowItem[]): { prefixes4: string[]; prefixes6: string[] } => {
    const prefixes = rows.map((r) => r.prefix);
    return {
        prefixes4: prefixes.filter((p) => !p.includes(':')),
        prefixes6: prefixes.filter((p) => p.includes(':')),
    };
};

/** Serialize prefix rows for the active config to YAML. */
export const rowsToYaml = (configName: string, rows: PrefixRowItem[]): string => {
    const doc: DecapYamlDoc = { config: configName, ...partitionRows(rows) };
    return dumpYamlDoc(doc);
};

/** Serialize prefix rows (without config wrapper) to YAML for diff display. */
export const rowsToDiffYaml = (rows: PrefixRowItem[]): string => dumpYamlDoc(partitionRows(rows));

/**
 * Parse YAML with family-typed `prefixes4`/`prefixes6` lists (with or
 * without the `config` wrapper) into prefix rows, IPv4 entries first.
 * Either list may be omitted, but not both. Throws with a descriptive
 * message on failure.
 */
export const yamlToRows = (text: string): PrefixRowItem[] => {
    const parse = (key: 'prefixes4' | 'prefixes6'): PrefixRowItem[] =>
        parseYamlList<PrefixRowItem>(text, key, (p) => ({
            prefix: typeof p === 'string' ? p : '',
        }));

    const tryParse = (key: 'prefixes4' | 'prefixes6'): PrefixRowItem[] | null => {
        try {
            return parse(key);
        } catch {
            return null;
        }
    };

    const v4 = tryParse('prefixes4');
    const v6 = tryParse('prefixes6');
    if (v4 === null && v6 === null) {
        // Surface the real parse error for the canonical key.
        return parse('prefixes4');
    }
    return [...(v4 ?? []), ...(v6 ?? [])];
};
