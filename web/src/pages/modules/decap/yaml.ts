import jsYaml from 'js-yaml';
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
    return jsYaml.dump(doc, { sortKeys: false, lineWidth: 120, noRefs: true });
};

/** Serialize prefix rows (without config wrapper) to YAML for diff display. */
export const rowsToDiffYaml = (rows: PrefixRowItem[]): string => {
    const doc = { prefixes: rows.map((r) => r.prefix) };
    return jsYaml.dump(doc, { sortKeys: false, lineWidth: 120, noRefs: true });
};

/**
 * Parse YAML (either { config, prefixes } or just { prefixes }) into prefix rows.
 * Returns the parsed rows. Throws with a descriptive message on failure.
 */
export const yamlToRows = (text: string): PrefixRowItem[] => {
    let parsed: unknown;
    try {
        parsed = jsYaml.load(text);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (!parsed || typeof parsed !== 'object') {
        throw new Error('Expected a YAML object with a "prefixes" list.');
    }

    const doc = parsed as Record<string, unknown>;
    const prefixesRaw = Array.isArray(doc['prefixes']) ? doc['prefixes'] : null;

    if (!prefixesRaw) {
        throw new Error('Expected a top-level "prefixes" list.');
    }

    return prefixesRaw.map((p: unknown, idx: number): PrefixRowItem => {
        const prefix = typeof p === 'string' ? p : '';
        return {
            id: `import-${idx}-${Date.now()}`,
            prefix,
        };
    });
};
