/**
 * Pure parse helpers shared between the main thread and the YAML import worker.
 *
 * No DOM, no React, no module side-effects beyond js-yaml.
 * Both hooks.ts and yamlImport.worker.ts import from here.
 */

import type { ProtoRange } from '@yanet/core/api/acl';
import { parseRangesRaw } from '@yanet/core/utils';

export { parseCidrsToIPNets, parseRangesRaw } from '@yanet/core/utils';

/** Parse encoded proto ranges (e.g. "1536-1791") to ProtoRange wire objects. */
export const parseProtoRangesRaw = (raw: string): ProtoRange[] => parseRangesRaw(raw) as ProtoRange[];
