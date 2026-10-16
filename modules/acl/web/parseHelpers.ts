/**
 * Pure parse helpers shared between the main thread and the YAML import worker.
 *
 * No DOM, no React, no module side-effects beyond js-yaml.
 * Both hooks.ts and yamlImport.worker.ts import from here.
 */

import type { ProtoRange, Protocol } from '@yanet/core/api/acl';
import { parseRangesRaw } from '@yanet/core/utils';

export { parseRangesRaw, partitionCidrsToTyped } from '@yanet/core/utils';

/** Parse encoded proto ranges (e.g. "1536-1791") to ProtoRange wire objects. */
export const parseProtoRangesRaw = (raw: string): ProtoRange[] => parseRangesRaw(raw) as ProtoRange[];

/**
 * Expand structured protocol entries to their encoded ProtoRange form.
 *
 * Mirrors the control-plane expansion: a TCP flag mask opens one range
 * per run of flag bytes it admits, ICMP types map to their blocks, an
 * unconstrained entry covers the whole protocol block.
 */
export const protocolEntriesToRanges = (entries: Protocol[] | undefined): ProtoRange[] => {
    const ranges: ProtoRange[] = [];
    for (const entry of entries ?? []) {
        const number = entry.number ?? 0;
        if (number > 255) continue;

        if (entry.tcp) {
            const flags = entry.tcp.flags ?? 0;
            const mask = entry.tcp.mask ?? 0;
            let runStart = -1;
            for (let byte = 0; byte <= 256; byte++) {
                const inRun = byte < 256 && (byte & mask) === (flags & mask);
                if (inRun && runStart < 0) {
                    runStart = byte;
                } else if (!inRun && runStart >= 0) {
                    ranges.push({ from: (6 << 8) | runStart, to: (6 << 8) | (byte - 1) });
                    runStart = -1;
                }
            }
            continue;
        }

        const typeRanges = entry.icmp_types?.length
            ? { proto: 1, list: entry.icmp_types }
            : entry.icmp6_types?.length
              ? { proto: 58, list: entry.icmp6_types }
              : null;
        if (typeRanges) {
            for (const typeRange of typeRanges.list) {
                // An absent bound is the proto3 zero: the gateway omits
                // a `to: 0` from the wire JSON.
                const from = typeRange.from ?? 0;
                const to = typeRange.to ?? 0;
                if (from <= to && from <= 255) {
                    const high = Math.min(to, 255);
                    ranges.push({ from: (typeRanges.proto << 8) | from, to: (typeRanges.proto << 8) | high });
                }
            }
            continue;
        }

        ranges.push({ from: number << 8, to: (number << 8) | 0xff });
    }
    return ranges;
};
