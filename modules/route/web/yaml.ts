import { dumpYamlDoc, parseYamlList } from '@yanet/core/utils';
import { cidrToIPRange } from '@yanet/core/utils/netip';
import type { FIBRowItem } from './types';

/** Single-config YAML shape: { config, routes: [...] }. Mirrors Forward's envelope style. */
interface FIBYamlRoute {
    from: string;
    to: string;
    dst_mac: string;
    src_mac: string;
    device: string;
    counter: string;
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
            from: r.from,
            to: r.to,
            dst_mac: r.dst_mac,
            src_mac: r.src_mac,
            device: r.device,
            counter: r.counter,
        })),
    };
    return dumpYamlDoc(doc);
};

/** Serialize FIB rows (without config wrapper) to YAML for diff display. */
export const rowsToDiffYaml = (rows: FIBRowItem[]): string => {
    const doc = {
        routes: rows.map((r) => ({
            from: r.from,
            to: r.to,
            dst_mac: r.dst_mac,
            src_mac: r.src_mac,
            device: r.device,
            counter: r.counter,
        })),
    };
    return dumpYamlDoc(doc);
};

/**
 * Parse YAML (either the full { config, routes } doc or just { routes }) into FIB rows.
 * Returns the parsed rows. Throws with a descriptive message on failure, including when
 * a row's counter is present but not a string — a malformed value must not silently
 * become the valid "auto-generate" empty string.
 *
 * A non-empty "prefix" with no from/to is a pre-range export. A valid CIDR converts
 * through cidrToIPRange — an invalid one is kept as raw text in "from" so the row still
 * imports and surfaces as invalid, the same way a malformed new-format row does.
 */
export const yamlToRows = (text: string): FIBRowItem[] =>
    parseYamlList<FIBRowItem>(text, 'routes', (r, idx) => {
        const row = (r && typeof r === 'object') ? (r as Record<string, unknown>) : {};
        const counterRaw = row['counter'];
        if (counterRaw !== undefined && typeof counterRaw !== 'string') {
            throw new Error(`Route #${idx + 1}: "counter" must be a string`);
        }
        let from = typeof row['from'] === 'string' ? row['from'] : '';
        let to = typeof row['to'] === 'string' ? row['to'] : '';
        const prefix = typeof row['prefix'] === 'string' ? row['prefix'] : '';
        if (!from && !to && prefix) {
            const range = cidrToIPRange(prefix);
            from = (range?.start && range.end) ? range.start : prefix;
            to = (range?.start && range.end) ? range.end : '';
        }
        return {
            from,
            to,
            dst_mac: typeof row['dst_mac'] === 'string' ? row['dst_mac'] : '',
            src_mac: typeof row['src_mac'] === 'string' ? row['src_mac'] : '',
            device: typeof row['device'] === 'string' ? row['device'] : '',
            counter: typeof counterRaw === 'string' ? counterRaw : '',
        };
    });
