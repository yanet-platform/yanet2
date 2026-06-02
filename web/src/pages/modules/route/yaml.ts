import { dumpYamlDoc, parseYamlList } from '../../_shared/draft/yaml';
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
    return dumpYamlDoc(doc);
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
    return dumpYamlDoc(doc);
};

/**
 * Parse YAML (either the full { config, routes } doc or just { routes }) into FIB rows.
 * Returns the parsed rows. Throws with a descriptive message on failure.
 */
export const yamlToRows = (text: string): FIBRowItem[] =>
    parseYamlList<FIBRowItem>(text, 'routes', (r) => {
        const row = (r && typeof r === 'object') ? (r as Record<string, unknown>) : {};
        return {
            prefix: typeof row['prefix'] === 'string' ? row['prefix'] : '',
            dst_mac: typeof row['dst_mac'] === 'string' ? row['dst_mac'] : '',
            src_mac: typeof row['src_mac'] === 'string' ? row['src_mac'] : '',
            device: typeof row['device'] === 'string' ? row['device'] : '',
        };
    });
