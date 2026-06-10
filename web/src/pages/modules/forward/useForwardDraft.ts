import { useCallback, useEffect, useMemo, useReducer, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { Rule } from '../../../api/forward';
import {
    forwardDraftReducer,
    initialDraftState,
} from './draftReducer';
import type { ForwardDraftAction } from './draftReducer';
import { useConfigPersistence, type ConfigPersistenceDispatch } from '../../../components/draft/useConfigPersistence';

const EMPTY_RULES: Rule[] = [];

const forwardUpdateConfig = (name: string, rules: Rule[]): Promise<unknown> =>
    API.forward.updateConfig({ name, rules });

const forwardDeleteConfig = (name: string): Promise<unknown> =>
    API.forward.deleteConfig({ name });

export interface UseForwardDraftResult {
    /** Union of server configs and local-only draft configs, minus pending-delete ones (for display). */
    draftConfigs: string[];
    /** Loading flag — true until the first server fetch completes. */
    loading: boolean;
    /** Returns the current draft rules for a config. */
    draftRules: (configName: string) => Rule[];
    /** Returns the server snapshot rules for a config. */
    serverRules: (configName: string) => Rule[];
    /** Returns true when the config has unsaved changes. */
    isDirty: (configName: string) => boolean;
    /** Returns true when any config has unsaved changes. */
    anyDirty: boolean;
    /** Dispatch a draft mutation. Does not touch the server. */
    dispatchDraft: (action: ForwardDraftAction) => void;
    /** Save one config to the server, then mark it clean. */
    saveConfig: (configName: string) => Promise<void>;
    /** Immediately delete a config: dispatches DELETE_CONFIG, calls the API for server configs, toasts on completion. */
    commitDeleteConfig: (configName: string) => Promise<void>;
    /** Revert one config's draft back to the server snapshot. */
    discardConfig: (configName: string) => void;
    /** Save all dirty configs sequentially. */
    saveAll: () => Promise<void>;
    /** Discard all dirty configs. */
    discardAll: () => void;
}

/**
 * Wraps forward config data with a local-draft layer.
 *
 * Server state is fetched once on mount via the inspect + showConfig APIs.
 * All UI mutations go through dispatchDraft and update only local state
 * until the user explicitly calls saveConfig. On save the full draft rule
 * list is written via API.forward.updateConfig (or deleteConfig for deletions),
 * then the local server snapshot is updated so dirty clears.
 */
export const useForwardDraft = (): UseForwardDraftResult => {
    const [state, rawDispatch] = useReducer(forwardDraftReducer, initialDraftState);
    const [loading, setLoading] = useState(true);

    const dispatchDraft = useCallback((action: ForwardDraftAction): void => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const inspectResp = await API.inspect.inspect();
            const cpConfigs = inspectResp.instance_info?.cp_configs ?? [];
            const forwardNames = cpConfigs
                .filter(cfg => cfg.type === 'forward')
                .map(cfg => cfg.name ?? '')
                .filter(Boolean);

            const configs: Array<{ name: string; rules: Rule[] }> = await Promise.all(
                forwardNames.map(async (name): Promise<{ name: string; rules: Rule[] }> => {
                    try {
                        const resp = await API.forward.showConfig({ name });
                        return { name, rules: resp.rules ?? [] };
                    } catch {
                        return { name, rules: [] };
                    }
                }),
            );

            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
        } catch (err) {
            toaster.error('yn-draft-load', 'Failed to load forward configurations', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const { saveConfig, commitDeleteConfig, discardConfig } = useConfigPersistence<Rule>({
        updateConfig: forwardUpdateConfig,
        deleteConfig: forwardDeleteConfig,
        toastKeyPrefix: 'yn-save',
        rollbackActionType: 'CANCEL_PENDING_DELETE',
        rawDispatch: rawDispatch as ConfigPersistenceDispatch,
        draft: state.draft,
        pendingDeleteConfigs: state.pendingDeleteConfigs,
        localOnlyConfigs: state.localOnlyConfigs,
    });

    const saveAll = useCallback(async (): Promise<void> => {
        const dirtyConfigs = [
            ...state.serverConfigs,
            ...state.localOnlyConfigs,
        ].filter(name => state.dirty.has(name));

        for (const name of dirtyConfigs) {
            await saveConfig(name);
        }
    }, [state.serverConfigs, state.localOnlyConfigs, state.dirty, saveConfig]);

    const discardAll = useCallback((): void => {
        const allDirty = [
            ...state.serverConfigs,
            ...state.localOnlyConfigs,
        ].filter(name => state.dirty.has(name));

        for (const name of allDirty) {
            rawDispatch({ type: 'DISCARD_CONFIG', configName: name });
        }
    }, [state.serverConfigs, state.localOnlyConfigs, state.dirty]);

    const draftRulesFor = useCallback((configName: string): Rule[] =>
        state.draft[configName] ?? EMPTY_RULES, [state.draft]);

    const serverRulesFor = useCallback((configName: string): Rule[] =>
        state.server[configName] ?? EMPTY_RULES, [state.server]);

    const isDirty = useCallback((configName: string): boolean =>
        state.dirty.has(configName), [state.dirty]);

    // Visible configs: server configs (minus pending deletes) plus local-only configs.
    const draftConfigs = useMemo(
        () => [
            ...state.serverConfigs.filter(n => !state.pendingDeleteConfigs.has(n)),
            ...state.localOnlyConfigs,
        ],
        [state.serverConfigs, state.pendingDeleteConfigs, state.localOnlyConfigs],
    );

    const anyDirty = state.dirty.size > 0;

    return {
        draftConfigs,
        loading,
        draftRules: draftRulesFor,
        serverRules: serverRulesFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
        saveAll,
        discardAll,
    };
};
