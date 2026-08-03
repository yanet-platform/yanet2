import { useCallback } from 'react';
import { API, inventoryConfigNames, loadKnownConfigs } from '@yanet/core/api';
import { warnConfigsUnknown } from '@yanet/core/utils';
import type { PrefixRowItem } from './types';
import { prefixDraftReducer, initialPrefixDraftState } from './prefixDraftReducer';
import { useDraft } from '@yanet/core/components/draft';
import type { UseDraftResult } from '@yanet/core/components/draft';

export type UsePrefixDraftResult = UseDraftResult<PrefixRowItem>;

/**
 * Wraps decap prefix config data with a local-draft layer.
 *
 * Server state is fetched once on mount. All UI mutations go through
 * dispatchDraft and update only local state until the user explicitly calls
 * commitConfig. On commit the full draft prefix list is sent atomically via
 * UpdateConfig, replacing the server state entirely.
 */
export const usePrefixDraft = (): UsePrefixDraftResult => {
    const load = useCallback(async (): Promise<Array<{ name: string; rows: PrefixRowItem[] }>> => {
        const configNames = await inventoryConfigNames('decap');
        return loadKnownConfigs(configNames, async (name): Promise<{ name: string; rows: PrefixRowItem[] }> => {
            const resp = await API.decap.showConfig({ name });
            const rows: PrefixRowItem[] = (resp.prefixes ?? []).map((p) => ({ id: p, prefix: p }));
            return { name, rows };
        }, { onDropped: warnConfigsUnknown('decap-configs-unknown', 'decap') });
    }, []);

    const commit = useCallback(async (
        configName: string,
        draftRows: PrefixRowItem[],
    ): Promise<void> => {
        await API.decap.updateConfig({ name: configName, prefixes: draftRows.map((r) => r.prefix) });
    }, []);

    return useDraft<PrefixRowItem>({
        load,
        commit,
        reducer: prefixDraftReducer,
        initialState: initialPrefixDraftState,
        toastSubject: 'prefix',
        errorSubject: 'Decap',
        cacheKey: 'decap',
    });
};
