import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { API, inventoryConfigNames, loadKnownConfigs, unionConfigNames } from '@yanet/core/api';
import { useConfigListCache } from '@yanet/core/hooks';
import { toaster, compareNatural, warnConfigsUnknown } from '@yanet/core/utils';
import type { Rule, SyncConfig } from '@yanet/core/api/acl';
import {
    aclDraftReducer,
    initialAclDraftState,
    EMPTY_FWTABLE_NAMES,
    type FwtableNames,
} from './draftReducer';
import type { AclDraftAction } from './draftReducer';
import { useConfigPersistence, type ConfigPersistenceDispatch } from '@yanet/core/components/draft/useConfigPersistence';

const EMPTY_RULES: Rule[] = [];
const EMPTY_IDS: string[] = [];

const aclDeleteConfig = (name: string): Promise<unknown> =>
    API.acl.deleteConfig({ name });

export interface UseAclDraftResult {
    draftConfigs: string[];
    loading: boolean;
    /** True when the initial load failed and no configs were seeded; cleared on a successful reload. */
    loadFailed: boolean;
    draftRules: (configName: string) => Rule[];
    draftRuleIds: (configName: string) => string[];
    serverRules: (configName: string) => Rule[];
    fwtableNames: (configName: string) => FwtableNames;
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
 * Server state is fetched once on mount via listConfigs and the shared-memory
 * inventory, then showConfig per name.
 * All UI mutations go through dispatchDraft and update only local state until
 * the user explicitly calls saveConfig.
 */
export const useAclDraft = (): UseAclDraftResult => {
    const [state, rawDispatch] = useReducer(aclDraftReducer, initialAclDraftState);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const { write: writeCache } = useConfigListCache('acl');

    // Latest stored sync config per name, read by the identity-stable save
    // wrapper below so a web save keeps the config's emission settings
    // instead of dropping them; the sync config has no editing UI yet.
    const serverSyncConfigRef = useRef<Record<string, SyncConfig | undefined>>({});
    serverSyncConfigRef.current = state.serverSyncConfig;
    // The map names are editable, so a save sends their draft values.
    const draftFwtableNameRef = useRef<Record<string, FwtableNames>>({});
    draftFwtableNameRef.current = state.draftFwtableName;

    const aclUpdateConfig = useCallback((name: string, rules: Rule[]): Promise<unknown> => {
        const fwtableName = draftFwtableNameRef.current[name] ?? EMPTY_FWTABLE_NAMES;
        return API.acl.updateConfig({
            name,
            rules,
            fwtable_name_v4: fwtableName.v4 || undefined,
            fwtable_name_v6: fwtableName.v6 || undefined,
            sync_config: serverSyncConfigRef.current[name],
        });
    }, []);

    const dispatchDraft = useCallback((action: AclDraftAction): void => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const [listResp, inventoryNames] = await Promise.all([
                API.acl.listConfigs(),
                inventoryConfigNames('acl'),
            ]);
            const names = unionConfigNames(listResp.configs ?? [], inventoryNames);

            const configs = await loadKnownConfigs(
                names,
                async (name): Promise<{ name: string; rules: Rule[]; fwtableName: FwtableNames; syncConfig?: SyncConfig }> => {
                    const resp = await API.acl.showConfig({ name });
                    return {
                        name,
                        rules: resp.rules ?? [],
                        fwtableName: {
                            v4: resp.fwtable_name_v4 ?? '',
                            v6: resp.fwtable_name_v6 ?? '',
                        },
                        syncConfig: resp.sync_config,
                    };
                },
                { onDropped: warnConfigsUnknown('acl-configs-unknown', 'ACL') },
            );

            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
            writeCache({
                configs: configs.map(cfg => cfg.name).sort((a, b) => compareNatural(a, b)),
                counts: Object.fromEntries(configs.map(cfg => [cfg.name, cfg.rules.length])),
            });
            setLoadFailed(false);
        } catch (err) {
            toaster.error('acl-load', 'Failed to load ACL configurations', err);
            setLoadFailed(true);
        } finally {
            setLoading(false);
        }
    }, [writeCache]);

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
    const fwtableNamesFor = useCallback((configName: string): FwtableNames =>
        state.draftFwtableName[configName] ?? EMPTY_FWTABLE_NAMES, [state.draftFwtableName]);

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
        loadFailed,
        draftRules: draftRulesFor,
        draftRuleIds: draftRuleIdsFor,
        serverRules: serverRulesFor,
        fwtableNames: fwtableNamesFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    };
};
