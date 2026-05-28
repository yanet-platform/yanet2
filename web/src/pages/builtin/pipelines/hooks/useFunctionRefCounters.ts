import { useCallback } from 'react';
import { API } from '../../../../api';
import { useInterpolatedCounters } from '../../../../hooks';
import type { InterpolatedCounterData } from '../../../../hooks';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../../utils';

/** Well-known key for pipeline-level fallthrough counters. */
export const PIPELINE_COUNTER_KEY = '__pipeline__';

export interface FunctionRefInfo {
    nodeId: string;
    functionName: string;
}

export interface UseFunctionRefCountersResult {
    counters: Map<string, InterpolatedCounterData>;
}

/**
 * Fetches and interpolates per-function-ref counters for a pipeline.
 *
 * Polls CountersService.ByTags each second and returns values keyed by
 * FunctionRef.id. When refs is empty, fetches pipeline-level counters
 * under PIPELINE_COUNTER_KEY.
 */
export const useFunctionRefCounters = (
    pipelineName: string,
    refs: FunctionRefInfo[],
): UseFunctionRefCountersResult => {
    const hasFunctionRefs = refs.length > 0;

    const keys: string[] = hasFunctionRefs
        ? refs.map(r => r.nodeId)
        : [PIPELINE_COUNTER_KEY];

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        if (!pipelineName) {
            return newValues;
        }

        if (hasFunctionRefs) {
            for (const ref of refs) {
                newValues.set(ref.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
            }

            try {
                const response = await API.counters.byTags({
                    tags: [
                        { key: 'pipeline', value: pipelineName },
                        { key: 'function', value: '*' },
                        { key: 'chain', value: '' },
                    ],
                    query: ['input', 'input_bytes'],
                });
                const grouped = groupCounterGroupsByTagsAndName(response.groups, ['pipeline', 'function'], 0);

                for (const ref of refs) {
                    const tagValues = [pipelineName, ref.functionName];
                    const packets = grouped.get(makeGroupedCounterKey(tagValues, 'input'))?.value ?? BigInt(0);
                    const bytes = grouped.get(makeGroupedCounterKey(tagValues, 'input_bytes'))?.value ?? BigInt(0);

                    newValues.set(ref.nodeId, { packets, bytes });
                }
            } catch {
                // tolerate fetch failures.
            }
        } else {
            newValues.set(PIPELINE_COUNTER_KEY, { packets: BigInt(0), bytes: BigInt(0) });

            try {
                const response = await API.counters.byTags({
                    tags: [
                        { key: 'pipeline', value: pipelineName },
                        { key: 'function', value: '' },
                    ],
                    query: ['input', 'input_bytes'],
                });
                const grouped = groupCounterGroupsByTagsAndName(response.groups, ['pipeline'], 0);
                newValues.set(PIPELINE_COUNTER_KEY, {
                    packets: grouped.get(makeGroupedCounterKey([pipelineName], 'input'))?.value ?? BigInt(0),
                    bytes: grouped.get(makeGroupedCounterKey([pipelineName], 'input_bytes'))?.value ?? BigInt(0),
                });
            } catch {
                // tolerate fetch failures.
            }
        }

        return newValues;
    }, [pipelineName, refs, hasFunctionRefs]);

    const { counters } = useInterpolatedCounters({
        keys,
        fetchCounters,
        enabled: pipelineName.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
