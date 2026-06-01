/**
 * Web Worker for off-main-thread YAML/JSON import.
 *
 * Message protocol:
 *   In  (main → worker): { type: 'parse', text: string, format?: 'yaml' | 'json' }
 *   Out (worker → main): { type: 'progress', stage: 'yaml' | 'rules', done: number, total: number }
 *                      | { type: 'done', rules: Rule[] }
 *                      | { type: 'error', message: string }
 */

import yaml from 'js-yaml';
import { ActionKind } from '../../../api/acl';
import type { Rule } from '../../../api/acl';
import { parseCidrsToIPNets } from './parseHelpers';

/** Raw shape of an action entry in the YAML schema. */
interface YamlAction {
    kind?: string;
}

/** Raw shape of a rule entry in the YAML schema. */
interface YamlRule {
    srcs?: unknown;
    dsts?: unknown;
    src_port_ranges?: unknown;
    dst_port_ranges?: unknown;
    proto_ranges?: unknown;
    vlan_ranges?: unknown;
    devices?: unknown;
    counter?: unknown;
    actions?: unknown;
}

const ACTION_KIND_FROM_STRING: Record<string, ActionKind> = {
    ACTION_KIND_PASS: ActionKind.ACTION_KIND_PASS,
    ACTION_KIND_DENY: ActionKind.ACTION_KIND_DENY,
    ACTION_KIND_COUNT: ActionKind.ACTION_KIND_COUNT,
    ACTION_KIND_CHECK_STATE: ActionKind.ACTION_KIND_CHECK_STATE,
    ACTION_KIND_CREATE_STATE: ActionKind.ACTION_KIND_CREATE_STATE,
    ACTION_KIND_LOG: ActionKind.ACTION_KIND_LOG,
};

const parseStringArray = (val: unknown): string[] => {
    if (!Array.isArray(val)) return [];
    return (val as unknown[]).filter((s): s is string => typeof s === 'string');
};

/** Convert an array of {from, to} YAML objects to wire range objects. */
const rangesFromObjects = (val: unknown): Array<{ from: number; to: number }> => {
    if (!Array.isArray(val)) return [];
    const results: Array<{ from: number; to: number }> = [];
    for (const item of val as unknown[]) {
        if (!item || typeof item !== 'object') continue;
        const obj = item as Record<string, unknown>;
        const from = typeof obj['from'] === 'number' ? obj['from'] : Number(obj['from'] ?? 0);
        const to = typeof obj['to'] === 'number' ? obj['to'] : Number(obj['to'] ?? 0);
        if (isNaN(from) || isNaN(to)) continue;
        results.push({ from, to });
    }
    return results;
};

const convertRow = (r: unknown): Rule => {
    if (!r || typeof r !== 'object') return {};
    const row = r as YamlRule;

    const srcs = parseCidrsToIPNets(parseStringArray(row.srcs));
    const dsts = parseCidrsToIPNets(parseStringArray(row.dsts));

    const src_port_ranges = rangesFromObjects(row.src_port_ranges);
    const dst_port_ranges = rangesFromObjects(row.dst_port_ranges);
    const proto_ranges = rangesFromObjects(row.proto_ranges);
    const vlan_ranges = rangesFromObjects(row.vlan_ranges);

    const devicesRaw = Array.isArray(row.devices) ? row.devices as unknown[] : [];
    const devices = devicesRaw
        .filter((d): d is Record<string, unknown> => !!d && typeof d === 'object')
        .map(d => ({ name: typeof d['name'] === 'string' ? d['name'] : '' }))
        .filter(d => d.name !== '');
    const counter = typeof row.counter === 'string' ? row.counter : undefined;

    const actionsRaw = Array.isArray(row.actions) ? row.actions as unknown[] : [];
    const actions = actionsRaw
        .filter((a): a is YamlAction => !!a && typeof a === 'object')
        .map((a): { kind: ActionKind } | null => {
            if (typeof a.kind !== 'string') return null;
            const kind = ACTION_KIND_FROM_STRING[a.kind];
            if (kind === undefined) return null;
            return { kind };
        })
        .filter((a): a is { kind: ActionKind } => a !== null);

    return { srcs, dsts, src_port_ranges, dst_port_ranges, proto_ranges, vlan_ranges, devices, counter, actions };
};

/** Convert raw rule rows in chunks, posting progress after each chunk. */
const convertRulesChunked = (rawRules: unknown[]): Promise<Rule[]> => {
    const CHUNK_SIZE = 2000;
    const total = rawRules.length;
    const results: Rule[] = new Array(total);

    return new Promise((resolve) => {
        let offset = 0;

        const processChunk = (): void => {
            const end = Math.min(offset + CHUNK_SIZE, total);
            for (let idx = offset; idx < end; idx++) {
                results[idx] = convertRow(rawRules[idx]);
            }
            offset = end;

            self.postMessage({ type: 'progress', stage: 'rules', done: offset, total });

            if (offset < total) {
                setTimeout(processChunk, 0);
            } else {
                resolve(results);
            }
        };

        processChunk();
    });
};

self.onmessage = async (e: MessageEvent<{ type: string; text: string; format?: 'yaml' | 'json' }>): Promise<void> => {
    if (e.data.type !== 'parse') return;

    const { text, format = 'yaml' } = e.data;

    self.postMessage({ type: 'progress', stage: 'yaml', done: 0, total: 1 });

    let parsed: unknown;
    try {
        if (format === 'json') {
            parsed = JSON.parse(text);
        } else {
            parsed = yaml.load(text);
        }
    } catch (err) {
        const label = format === 'json' ? 'JSON parse error' : 'YAML parse error';
        self.postMessage({ type: 'error', message: `${label}: ${(err as Error).message}` });
        return;
    }

    self.postMessage({ type: 'progress', stage: 'yaml', done: 1, total: 1 });

    if (!parsed || typeof parsed !== 'object') {
        self.postMessage({ type: 'error', message: 'Expected an object with a "rules" list.' });
        return;
    }

    const doc = parsed as Record<string, unknown>;
    if (!Array.isArray(doc['rules'])) {
        self.postMessage({ type: 'error', message: 'Expected a top-level "rules" list.' });
        return;
    }

    const rawRules = doc['rules'] as unknown[];

    try {
        const rules = await convertRulesChunked(rawRules);
        self.postMessage({ type: 'done', rules });
    } catch (err) {
        self.postMessage({ type: 'error', message: `Conversion error: ${(err as Error).message}` });
    }
};
