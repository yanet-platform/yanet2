import type { Rule } from '../../../api/acl-ng';

/** Monotonically increasing counter for generating stable tmp- ids. */
let tmpIdCounter = 0;
const nextTmpId = (): string => `tmp-${++tmpIdCounter}`;

/** Assign stable server ids to a rules array. */
const serverIds = (rules: Rule[]): string[] => rules.map((_, idx) => `srv-${idx}`);

export interface AclNgDraftState {
    server: Record<string, Rule[]>;
    draft: Record<string, Rule[]>;
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

export const initialAclNgDraftState: AclNgDraftState = {
    server: {},
    draft: {},
    draftIds: {},
    serverConfigs: [],
    localOnlyConfigs: [],
    pendingDeleteConfigs: new Set(),
    dirty: new Set(),
};

export type AclNgDraftAction =
    | { type: 'LOAD_ALL_CONFIGS'; configs: Array<{ name: string; rules: Rule[] }> }
    | { type: 'ADD_RULE'; configName: string; rule: Rule }
    | { type: 'UPDATE_RULE_AT_INDEX'; configName: string; index: number; rule: Rule }
    | { type: 'REMOVE_RULES'; configName: string; indices: number[] }
    | { type: 'REPLACE_ALL_RULES'; configName: string; rules: Rule[] }
    | { type: 'ADD_CONFIG'; configName: string }
    | { type: 'DELETE_CONFIG'; configName: string }
    | { type: 'DISCARD_CONFIG'; configName: string }
    | { type: 'MARK_SAVED'; configName: string };

export const aclNgDraftReducer = (
    state: AclNgDraftState,
    action: AclNgDraftAction,
): AclNgDraftState => {
    switch (action.type) {
        case 'LOAD_ALL_CONFIGS': {
            const newServer: Record<string, Rule[]> = { ...state.server };
            const newDraft: Record<string, Rule[]> = { ...state.draft };
            const newDraftIds: Record<string, string[]> = { ...state.draftIds };
            const serverConfigs: string[] = [];
            // Use reference equality to detect whether the user has local edits:
            // if draft[name] === server[name] the config was never mutated locally,
            // so it is safe to fast-forward to the new server snapshot.
            for (const { name, rules } of action.configs) {
                newServer[name] = rules;
                if (state.draft[name] === state.server[name]) {
                    newDraft[name] = rules;
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
                draft: newDraft,
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
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.delete(action.configName);
            if (serverRules === undefined) {
                // Local-only config: discard means remove it entirely.
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const { [action.configName]: _di, ...draftIdsRest } = state.draftIds;
                return {
                    ...state,
                    draft: draftRest,
                    draftIds: draftIdsRest,
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                    pendingDeleteConfigs,
                    dirty: nextDirty,
                };
            }
            return {
                ...state,
                draft: { ...state.draft, [action.configName]: serverRules },
                draftIds: { ...state.draftIds, [action.configName]: serverIds(serverRules) },
                pendingDeleteConfigs,
                dirty: nextDirty,
            };
        }

        case 'MARK_SAVED': {
            const savedRules = state.draft[action.configName];
            const wasPendingDelete = state.pendingDeleteConfigs.has(action.configName);
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);
            const nextDirty = new Set(state.dirty);
            nextDirty.delete(action.configName);

            if (wasPendingDelete || savedRules === undefined) {
                // Config was pending deletion (or never had a draft entry) and is now
                // gone from the server.
                const { [action.configName]: _s, ...serverRest } = state.server;
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const { [action.configName]: _di, ...draftIdsRest } = state.draftIds;
                return {
                    ...state,
                    server: serverRest,
                    draft: draftRest,
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
