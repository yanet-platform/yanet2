import { useCallback, useEffect, useReducer, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { Rule } from '../../../api/acl-ng';
import {
    aclNgDraftReducer,
    initialAclNgDraftState,
} from './draftReducer';
import type { AclNgDraftAction } from './draftReducer';

const EMPTY_RULES: Rule[] = [];
const EMPTY_IDS: string[] = [];

export interface UseAclNgDraftResult {
    draftConfigs: string[];
    loading: boolean;
    draftRules: (configName: string) => Rule[];
    draftRuleIds: (configName: string) => string[];
    serverRules: (configName: string) => Rule[];
    isDirty: (configName: string) => boolean;
    anyDirty: boolean;
    dispatchDraft: (action: AclNgDraftAction) => void;
    saveConfig: (configName: string) => Promise<void>;
    commitDeleteConfig: (configName: string) => Promise<void>;
    discardConfig: (configName: string) => void;
}

/**
 * Wraps ACL NG config data with a local-draft layer.
 *
 * Server state is fetched once on mount via listConfigs + showConfig per name.
 * All UI mutations go through dispatchDraft and update only local state until
 * the user explicitly calls saveConfig.
 */
export const useAclNgDraft = (): UseAclNgDraftResult => {
    const [state, rawDispatch] = useReducer(aclNgDraftReducer, initialAclNgDraftState);
    const [loading, setLoading] = useState(true);

    const dispatchDraft = useCallback((action: AclNgDraftAction): void => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const listResp = await API.aclng.listConfigs();
            const names = listResp.configs ?? [];

            const configs = await Promise.all(
                names.map(async (name): Promise<{ name: string; rules: Rule[] }> => {
                    try {
                        const resp = await API.aclng.showConfig({ name });
                        return { name, rules: resp.rules ?? [] };
                    } catch {
                        return { name, rules: [] };
                    }
                }),
            );

            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
        } catch (err) {
            toaster.error('acl-ng-load', 'Failed to load ACL NG configurations', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const saveConfig = useCallback(async (configName: string): Promise<void> => {
        const isPendingDelete = state.pendingDeleteConfigs.has(configName);

        if (isPendingDelete) {
            try {
                await API.aclng.deleteConfig({ name: configName });
                rawDispatch({ type: 'MARK_SAVED', configName });
                toaster.success(`acl-ng-save-${configName}`, `Config "${configName}" deleted.`);
            } catch (err) {
                toaster.error(`acl-ng-save-err-${configName}`, `Failed to delete "${configName}"`, err);
                throw err;
            }
            return;
        }

        const rules = state.draft[configName] ?? [];
        try {
            await API.aclng.updateConfig({ name: configName, rules });
            rawDispatch({ type: 'MARK_SAVED', configName });
            toaster.success(`acl-ng-save-${configName}`, `Config "${configName}" saved.`);
        } catch (err) {
            toaster.error(`acl-ng-save-err-${configName}`, `Failed to save "${configName}"`, err);
            throw err;
        }
    }, [state.draft, state.pendingDeleteConfigs]);

    const commitDeleteConfig = useCallback(async (configName: string): Promise<void> => {
        const isLocalOnly = state.localOnlyConfigs.includes(configName);
        rawDispatch({ type: 'DELETE_CONFIG', configName });
        if (isLocalOnly) {
            return;
        }
        try {
            await API.aclng.deleteConfig({ name: configName });
            rawDispatch({ type: 'MARK_SAVED', configName });
            toaster.success(`acl-ng-save-${configName}`, `Config "${configName}" deleted.`);
        } catch (err) {
            rawDispatch({ type: 'DISCARD_CONFIG', configName });
            toaster.error(`acl-ng-save-err-${configName}`, `Failed to delete "${configName}"`, err);
            throw err;
        }
    }, [state.localOnlyConfigs]);

    const discardConfig = useCallback((configName: string): void => {
        rawDispatch({ type: 'DISCARD_CONFIG', configName });
    }, []);

    const draftRulesFor = useCallback((configName: string): Rule[] =>
        state.draft[configName] ?? EMPTY_RULES, [state.draft]);

    const draftRuleIdsFor = useCallback((configName: string): string[] =>
        state.draftIds[configName] ?? EMPTY_IDS, [state.draftIds]);

    const serverRulesFor = useCallback((configName: string): Rule[] =>
        state.server[configName] ?? EMPTY_RULES, [state.server]);

    const isDirty = useCallback((configName: string): boolean =>
        state.dirty.has(configName), [state.dirty]);

    const draftConfigs = [
        ...state.serverConfigs.filter(n => !state.pendingDeleteConfigs.has(n)),
        ...state.localOnlyConfigs,
    ].sort((a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' }));

    const anyDirty = state.dirty.size > 0;

    return {
        draftConfigs,
        loading,
        draftRules: draftRulesFor,
        draftRuleIds: draftRuleIdsFor,
        serverRules: serverRulesFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    };
};
