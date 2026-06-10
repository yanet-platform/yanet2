import { useCallback, useEffect, useReducer, useState } from 'react';
import { API } from '../../../api';
import { toaster, compareNatural } from '../../../utils';
import type { Rule } from '../../../api/acl';
import {
    aclDraftReducer,
    initialAclDraftState,
} from './draftReducer';
import type { AclDraftAction } from './draftReducer';
import { useConfigPersistence, type ConfigPersistenceDispatch } from '../../../components/draft/useConfigPersistence';

const EMPTY_RULES: Rule[] = [];
const EMPTY_IDS: string[] = [];
const EMPTY_FWSTATE_NAME = '';

const aclUpdateConfig = (name: string, rules: Rule[]): Promise<unknown> =>
    API.acl.updateConfig({ name, rules });

const aclDeleteConfig = (name: string): Promise<unknown> =>
    API.acl.deleteConfig({ name });

export interface UseAclDraftResult {
    draftConfigs: string[];
    loading: boolean;
    draftRules: (configName: string) => Rule[];
    draftRuleIds: (configName: string) => string[];
    serverRules: (configName: string) => Rule[];
    fwstateName: (configName: string) => string;
    isDirty: (configName: string) => boolean;
    anyDirty: boolean;
    dispatchDraft: (action: AclDraftAction) => void;
    saveConfig: (configName: string) => Promise<void>;
    commitDeleteConfig: (configName: string) => Promise<void>;
    discardConfig: (configName: string) => void;
}

/**
 * Wraps ACL config data with a local-draft layer.
 *
 * Server state is fetched once on mount via listConfigs + showConfig per name.
 * All UI mutations go through dispatchDraft and update only local state until
 * the user explicitly calls saveConfig.
 */
export const useAclDraft = (): UseAclDraftResult => {
    const [state, rawDispatch] = useReducer(aclDraftReducer, initialAclDraftState);
    const [loading, setLoading] = useState(true);

    const dispatchDraft = useCallback((action: AclDraftAction): void => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const listResp = await API.acl.listConfigs();
            const names = listResp.configs ?? [];

            const configs = await Promise.all(
                names.map(async (name): Promise<{ name: string; rules: Rule[]; fwstateName: string }> => {
                    try {
                        const resp = await API.acl.showConfig({ name });
                        return { name, rules: resp.rules ?? [], fwstateName: resp.fwstate_name ?? '' };
                    } catch {
                        return { name, rules: [], fwstateName: '' };
                    }
                }),
            );

            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
        } catch (err) {
            toaster.error('acl-load', 'Failed to load ACL configurations', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const { saveConfig, commitDeleteConfig, discardConfig } = useConfigPersistence<Rule>({
        updateConfig: aclUpdateConfig,
        deleteConfig: aclDeleteConfig,
        toastKeyPrefix: 'acl-save',
        rollbackActionType: 'DISCARD_CONFIG',
        rawDispatch: rawDispatch as ConfigPersistenceDispatch,
        draft: state.draft,
        pendingDeleteConfigs: state.pendingDeleteConfigs,
        localOnlyConfigs: state.localOnlyConfigs,
    });

    const draftRulesFor = useCallback((configName: string): Rule[] =>
        state.draft[configName] ?? EMPTY_RULES, [state.draft]);

    const draftRuleIdsFor = useCallback((configName: string): string[] =>
        state.draftIds[configName] ?? EMPTY_IDS, [state.draftIds]);

    const serverRulesFor = useCallback((configName: string): Rule[] =>
        state.server[configName] ?? EMPTY_RULES, [state.server]);
    const fwstateNameFor = useCallback((configName: string): string =>
        state.serverFwStateName[configName] ?? EMPTY_FWSTATE_NAME, [state.serverFwStateName]);

    const isDirty = useCallback((configName: string): boolean =>
        state.dirty.has(configName), [state.dirty]);

    const draftConfigs = [
        ...state.serverConfigs.filter(n => !state.pendingDeleteConfigs.has(n)),
        ...state.localOnlyConfigs,
    ].sort((a, b) => compareNatural(a, b));

    const anyDirty = state.dirty.size > 0;

    return {
        draftConfigs,
        loading,
        draftRules: draftRulesFor,
        draftRuleIds: draftRuleIdsFor,
        serverRules: serverRulesFor,
        fwstateName: fwstateNameFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    };
};
