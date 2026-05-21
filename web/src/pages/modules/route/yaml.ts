import jsYaml from 'js-yaml';
import type { FIBRowItem } from './types';

/** Single-config YAML shape: { config, routes: [...] }. Mirrors Forward's envelope style. */
interface FIBYamlRoute {
    prefix: string;
    dst_mac: string;
    src_mac: string;
    device: string;
}

interface FIBYamlDoc {
    config: string;
    routes: FIBYamlRoute[];
}

/** Serialize FIB rows for the active config to YAML. */
export const rowsToYaml = (configName: string, rows: FIBRowItem[]): string => {
    const doc: FIBYamlDoc = {
        config: configName,
        routes: rows.map((r) => ({
            prefix: r.prefix,
            dst_mac: r.dst_mac,
            src_mac: r.src_mac,
            device: r.device,
        })),
    };
    return jsYaml.dump(doc, { sortKeys: false, lineWidth: 120, noRefs: true });
};

/** Serialize FIB rows (without config wrapper) to YAML for diff display. */
export const rowsToDiffYaml = (rows: FIBRowItem[]): string => {
    const doc = {
        routes: rows.map((r) => ({
            prefix: r.prefix,
            dst_mac: r.dst_mac,
            src_mac: r.src_mac,
            device: r.device,
        })),
    };
    return jsYaml.dump(doc, { sortKeys: false, lineWidth: 120, noRefs: true });
};

/**
 * Parse YAML (either the full { config, routes } doc or just { routes }) into FIB rows.
 * Returns the parsed rows. Throws with a descriptive message on failure.
 */
export const yamlToRows = (text: string): FIBRowItem[] => {
    let parsed: unknown;
    try {
        parsed = jsYaml.load(text);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (!parsed || typeof parsed !== 'object') {
        throw new Error('Expected a YAML object with a "routes" list.');
    }

    const doc = parsed as Record<string, unknown>;
    const routesRaw = Array.isArray(doc['routes']) ? doc['routes'] : null;

    if (!routesRaw) {
        throw new Error('Expected a top-level "routes" list.');
    }

    return routesRaw.map((r: unknown, idx: number): FIBRowItem => {
        const row = (r && typeof r === 'object') ? (r as Record<string, unknown>) : {};
        return {
            id: `import-${idx}-${Date.now()}`,
            prefix: typeof row['prefix'] === 'string' ? row['prefix'] : '',
            dst_mac: typeof row['dst_mac'] === 'string' ? row['dst_mac'] : '',
            src_mac: typeof row['src_mac'] === 'string' ? row['src_mac'] : '',
            device: typeof row['device'] === 'string' ? row['device'] : '',
        };
    });
};
