import type { Rule } from '@yanet/core/api/acl';
import type { SyncConfigFormFields } from '@yanet/core/utils';

/** Monotonically increasing counter for generating stable tmp- ids. */
let tmpIdCounter = 0;
const nextTmpId = (): string => `tmp-${++tmpIdCounter}`;

/** Assign stable server ids to a rules array. */
const serverIds = (rules: Rule[]): string[] => rules.map((_, idx) => `srv-${idx}`);

/**
 * Config-level map + sync draft, edited alongside rules.
 *
 * `mapName` references the standalone fwstate-map; `sync` holds the form-level
 * sync-config fields. Both are required on save iff any rule uses
 * ACTION_KIND_CREATE_STATE; the save path strips them otherwise.
 */
export interface AclMapSyncDraft {
    mapName: string;
    sync: SyncConfigFormFields;
}

export interface AclDraftState {
    server: Record<string, Rule[]>;
    serverMapSync: Record<string, AclMapSyncDraft>;
    draft: Record<string, Rule[]>;
    draftMapSync: Record<string, AclMapSyncDraft>;
    /**
     * Stable row ids parallel to draft[configName].
     * server-loaded rules: "srv-N"; locally-added rules: "tmp-N".
     * Preserved across UPDATE_RULE_AT_INDEX and REMOVE_RULES so that
     * the structured diff can match rows across mutations.
     */
    draftIds: Record<string, string[]>;
    serverConfigs: string[];
    localOnlyConfigs: string[];
    pendingDeleteConfigs: Set<string>;
    dirty: Set<string>;
}

export const initialAclDraftState: AclDraftState = {
    server: {},
    serverMapSync: {},
    draft: {},
    draftMapSync: {},
    draftIds: {},
    serverConfigs: [],
    localOnlyConfigs: [],
    pendingDeleteConfigs: new Set(),
    dirty: new Set(),
};

export type AclDraftAction =
    | { type: 'LOAD_ALL_CONFIGS'; configs: Array<{ name: string; rules: Rule[]; mapSync: AclMapSyncDraft }> }
    | { type: 'ADD_RULE'; configName: string; rule: Rule }
    | { type: 'UPDATE_RULE_AT_INDEX'; configName: string; index: number; rule: Rule }
    | { type: 'REMOVE_RULES'; configName: string; indices: number[] }
    | { type: 'REPLACE_ALL_RULES'; configName: string; rules: Rule[] }
    | { type: 'SET_MAP_SYNC'; configName: string; mapSync: AclMapSyncDraft }
    | { type: 'ADD_CONFIG'; configName: string }
    | { type: 'DELETE_CONFIG'; configName: string }
    | { type: 'DISCARD_CONFIG'; configName: string }
    | { type: 'MARK_SAVED'; configName: string };

export const aclDraftReducer = (
    state: AclDraftState,
    action: AclDraftAction,
): AclDraftState => {
    switch (action.type) {
        case 'LOAD_ALL_CONFIGS': {
            const newServer: Record<string, Rule[]> = { ...state.server };
            const newServerMapSync: Record<string, AclMapSyncDraft> = { ...state.serverMapSync };
            const newDraft: Record<string, Rule[]> = { ...state.draft };
            const newDraftMapSync: Record<string, AclMapSyncDraft> = { ...state.draftMapSync };
            const newDraftIds: Record<string, string[]> = { ...state.draftIds };
            const serverConfigs: string[] = [];
            // Use reference equality to detect whether the user has local edits:
            // if draft[name] === server[name] (and the map+sync draft is still
            // the server snapshot) the config was never mutated locally, so it
            // is safe to fast-forward to the new server snapshot.
            for (const { name, rules, mapSync } of action.configs) {
                newServer[name] = rules;
                newServerMapSync[name] = mapSync;
                if (state.draft[name] === state.server[name]
                    && state.draftMapSync[name] === state.serverMapSync[name]) {
                    newDraft[name] = rules;
                    newDraftMapSync[name] = mapSync;
                    newDraftIds[name] = serverIds(rules);
                }
                serverConfigs.push(name);
            }
            // Configs fresh from the server are never dirty; local-only and
            // pending-delete configs retain whatever dirty state they had before.
            const nextDirty = new Set(state.dirty);
            for (const { name } of action.configs) {
                if (!state.localOnlyConfigs.includes(name) && !state.pendingDeleteConfigs.has(name)) {
                    nextDirty.delete(name);
                }
            }
            return {
                ...state,
                server: newServer,
                serverMapSync: newServerMapSync,
                draft: newDraft,
                draftMapSync: newDraftMapSync,
                draftIds: newDraftIds,
                serverConfigs,
                dirty: nextDirty,
            };
        }

        case 'ADD_RULE': {
            const current = state.draft[action.configName] ?? [];
            const currentIds = state.draftIds[action.configName] ?? [];
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: [...current, action.rule] },
                draftIds: { ...state.draftIds, [action.configName]: [...currentIds, nextTmpId()] },
                dirty: nextDirty,
            };
        }

        case 'UPDATE_RULE_AT_INDEX': {
            const current = state.draft[action.configName] ?? [];
            const updated = [...current];
            updated[action.index] = action.rule;
            // Id is preserved — row identity doesn't change on edit.
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: updated },
                dirty: nextDirty,
            };
        }

        case 'REMOVE_RULES': {
            const current = state.draft[action.configName] ?? [];
            const currentIds = state.draftIds[action.configName] ?? [];
            const toRemove = new Set(action.indices);
            const updated = current.filter((_, idx) => !toRemove.has(idx));
            const updatedIds = currentIds.filter((_, idx) => !toRemove.has(idx));
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: updated },
                draftIds: { ...state.draftIds, [action.configName]: updatedIds },
                dirty: nextDirty,
            };
        }

        case 'REPLACE_ALL_RULES': {
            const isNew = !state.serverConfigs.includes(action.configName)
                && !state.localOnlyConfigs.includes(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: action.rules },
                draftIds: { ...state.draftIds, [action.configName]: action.rules.map(() => nextTmpId()) },
                localOnlyConfigs: isNew
                    ? [...state.localOnlyConfigs, action.configName]
                    : state.localOnlyConfigs,
                dirty: nextDirty,
            };
        }

        case 'SET_MAP_SYNC': {
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draftMapSync: { ...state.draftMapSync, [action.configName]: action.mapSync },
                dirty: nextDirty,
            };
        }

        case 'ADD_CONFIG': {
            if (
                state.serverConfigs.includes(action.configName)
                || state.localOnlyConfigs.includes(action.configName)
            ) {
                return state;
            }
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: [] },
                draftIds: { ...state.draftIds, [action.configName]: [] },
                localOnlyConfigs: [...state.localOnlyConfigs, action.configName],
                dirty: nextDirty,
            };
        }

        case 'DELETE_CONFIG': {
            const isLocalOnly = state.localOnlyConfigs.includes(action.configName);
            if (isLocalOnly) {
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const { [action.configName]: _di, ...draftIdsRest } = state.draftIds;
                const nextDirty = new Set(state.dirty);
                nextDirty.delete(action.configName);
                return {
                    ...state,
                    draft: draftRest,
                    draftIds: draftIdsRest,
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                    dirty: nextDirty,
                };
            }
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.add(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.add(action.configName);
            return { ...state, pendingDeleteConfigs, dirty: nextDirty };
        }

        case 'DISCARD_CONFIG': {
            const serverRules = state.server[action.configName];
            const serverMapSync = state.serverMapSync[action.configName];
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.delete(action.configName);
            if (serverRules === undefined) {
                // Local-only config: discard means remove it entirely.
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const { [action.configName]: _dm, ...draftMapSyncRest } = state.draftMapSync;
                const { [action.configName]: _di, ...draftIdsRest } = state.draftIds;
                return {
                    ...state,
                    draft: draftRest,
                    draftMapSync: draftMapSyncRest,
                    draftIds: draftIdsRest,
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                    pendingDeleteConfigs,
                    dirty: nextDirty,
                };
            }
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: serverRules },
                draftMapSync: { ...state.draftMapSync, [action.configName]: serverMapSync },
                draftIds: { ...state.draftIds, [action.configName]: serverIds(serverRules) },
                pendingDeleteConfigs,
                dirty: nextDirty,
            };
        }

        case 'MARK_SAVED': {
            const savedRules = state.draft[action.configName];
            const savedMapSync = state.draftMapSync[action.configName];
            const wasPendingDelete = state.pendingDeleteConfigs.has(action.configName);
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.delete(action.configName);

            if (wasPendingDelete || savedRules === undefined) {
                // Config was pending deletion (or never had a draft entry) and is now
                // gone from the server.
                const { [action.configName]: _s, ...serverRest } = state.server;
                const { [action.configName]: _sm, ...serverMapSyncRest } = state.serverMapSync;
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const { [action.configName]: _dm, ...draftMapSyncRest } = state.draftMapSync;
                const { [action.configName]: _di, ...draftIdsRest } = state.draftIds;
                return {
                    ...state,
                    server: serverRest,
                    serverMapSync: serverMapSyncRest,
                    draft: draftRest,
                    draftMapSync: draftMapSyncRest,
                    draftIds: draftIdsRest,
                    serverConfigs: state.serverConfigs.filter(n => n !== action.configName),
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                    pendingDeleteConfigs,
                    dirty: nextDirty,
                };
            }

            // Advance server snapshot to match what was just persisted so that
            // subsequent LOAD_ALL_CONFIGS can use reference equality correctly.
            return {
                ...state,
                server: { ...state.server, [action.configName]: savedRules },
                serverMapSync: { ...state.serverMapSync, [action.configName]: savedMapSync },
                serverConfigs: state.serverConfigs.includes(action.configName)
                    ? state.serverConfigs
                    : [...state.serverConfigs, action.configName],
                localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                pendingDeleteConfigs,
                dirty: nextDirty,
            };
        }

        default:
            return state;
    }
};
