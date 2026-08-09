import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { API, inventoryConfigNames, loadKnownConfigs, unionConfigNames } from '@yanet/core/api';
import { useConfigListCache } from '@yanet/core/hooks';
import { toaster, compareNatural, warnConfigsUnknown, syncConfigToFormFields, formFieldsToSyncConfig } from '@yanet/core/utils';
import type { Rule, SyncConfig } from '@yanet/core/api/acl';
import { ActionKind } from '@yanet/core/api/acl';
import {
    aclDraftReducer,
    initialAclDraftState,
} from './draftReducer';
import type { AclDraftAction, AclMapSyncDraft } from './draftReducer';
import { useConfigPersistence, type ConfigPersistenceDispatch } from '@yanet/core/components/draft/useConfigPersistence';

const EMPTY_RULES: Rule[] = [];
const EMPTY_IDS: string[] = [];
const EMPTY_MAP_SYNC: AclMapSyncDraft = { mapName: '', sync: syncConfigToFormFields(undefined) };

// Report whether any rule uses ACTION_KIND_CREATE_STATE — the action that
// drives fwstate sync packet emission and therefore requires map_name +
// sync_config on the wire. Mirrors the server-side validation contract.
const rulesNeedCreateState = (rules: Rule[]): boolean =>
    rules.some((rule) => (rule.actions ?? []).some((action) => action.kind === ActionKind.ACTION_KIND_CREATE_STATE));

const mapSyncFromWire = (mapName: string | undefined, syncConfig: SyncConfig | undefined): AclMapSyncDraft => ({
    mapName: mapName ?? '',
    sync: syncConfigToFormFields(syncConfig),
});

export interface UseAclDraftResult {
    draftConfigs: string[];
    loading: boolean;
    /** True when the initial load failed and no configs were seeded; cleared on a successful reload. */
    loadFailed: boolean;
    draftRules: (configName: string) => Rule[];
    draftRuleIds: (configName: string) => string[];
    serverRules: (configName: string) => Rule[];
    draftMapSync: (configName: string) => AclMapSyncDraft;
    serverMapSync: (configName: string) => AclMapSyncDraft;
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

    // Mirror the draft map+sync into a ref so the stable updateConfig wrapper
    // can read the latest values without churning its identity.
    const draftMapSyncRef = useRef(state.draftMapSync);
    useEffect(() => { draftMapSyncRef.current = state.draftMapSync; }, [state.draftMapSync]);

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
                async (name): Promise<{ name: string; rules: Rule[]; mapSync: AclMapSyncDraft }> => {
                    const resp = await API.acl.showConfig({ name });
                    return {
                        name,
                        rules: resp.rules ?? [],
                        mapSync: mapSyncFromWire(resp.map_name, resp.sync_config),
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

    // Build the wire UpdateConfigRequest. map_name is sent whenever the
    // draft carries one so a CHECK_STATE-only ruleset keeps borrowing the
    // standalone map; sync_config is sent only for CREATE_STATE, which is
    // the only action that emits sync packets.
    const updateConfig = useCallback(async (name: string, rules: Rule[]): Promise<unknown> => {
        const mapSync = draftMapSyncRef.current[name];
        const needsCreateState = rulesNeedCreateState(rules);
        const mapName = mapSync?.mapName;
        if (mapName) {
            return API.acl.updateConfig({
                name,
                rules,
                map_name: mapName,
                sync_config: needsCreateState ? formFieldsToSyncConfig(mapSync.sync) : undefined,
            });
        }
        return API.acl.updateConfig({ name, rules });
    }, []);

    const deleteConfig = useCallback((name: string): Promise<unknown> =>
        API.acl.deleteConfig({ name }), []);

    const { saveConfig, commitDeleteConfig, discardConfig } = useConfigPersistence<Rule>({
        updateConfig,
        deleteConfig,
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
    const draftMapSyncFor = useCallback((configName: string): AclMapSyncDraft =>
        state.draftMapSync[configName] ?? EMPTY_MAP_SYNC, [state.draftMapSync]);
    const serverMapSyncFor = useCallback((configName: string): AclMapSyncDraft =>
        state.serverMapSync[configName] ?? EMPTY_MAP_SYNC, [state.serverMapSync]);

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
        draftMapSync: draftMapSyncFor,
        serverMapSync: serverMapSyncFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    };
};
