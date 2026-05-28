import type { Rule } from '../../../api/forward';

export interface ForwardDraftState {
    /** Server-authoritative rule snapshots, keyed by config name. */
    server: Record<string, Rule[]>;
    /** Local draft rule sets, keyed by config name. Includes draft-only configs. */
    draft: Record<string, Rule[]>;
    /** Config names known on the server (loaded on init). */
    serverConfigs: string[];
    /** Config names created locally but not yet saved. */
    localOnlyConfigs: string[];
    /** Config names that exist on the server but are marked for deletion on next save. */
    pendingDeleteConfigs: Set<string>;
    /** Which config names have unsaved edits. Recomputed on every mutation. */
    dirty: Set<string>;
}

export const initialDraftState: ForwardDraftState = {
    server: {},
    draft: {},
    serverConfigs: [],
    localOnlyConfigs: [],
    pendingDeleteConfigs: new Set(),
    dirty: new Set(),
};

export type ForwardDraftAction =
    | { type: 'LOAD_SERVER'; configName: string; rules: Rule[] }
    | { type: 'LOAD_ALL_CONFIGS'; configs: Array<{ name: string; rules: Rule[] }> }
    | { type: 'ADD_RULE'; configName: string; rule: Rule }
    | { type: 'UPDATE_RULE_AT_INDEX'; configName: string; index: number; rule: Rule }
    | { type: 'REMOVE_RULES'; configName: string; indices: number[] }
    | { type: 'DUPLICATE_RULE'; configName: string; index: number }
    | { type: 'REPLACE_ALL_RULES'; configName: string; rules: Rule[] }
    | { type: 'ADD_CONFIG'; configName: string }
    | { type: 'DELETE_CONFIG'; configName: string }
    | { type: 'DISCARD_CONFIG'; configName: string }
    | { type: 'MARK_SAVED'; configName: string };

/** Recompute the dirty set by comparing JSON of draft vs server for every known config. */
const recomputeDirty = (state: Omit<ForwardDraftState, 'dirty'>): Set<string> => {
    const dirty = new Set<string>();
    const allConfigs = new Set([
        ...state.serverConfigs,
        ...state.localOnlyConfigs,
    ]);
    for (const name of allConfigs) {
        if (state.pendingDeleteConfigs.has(name)) {
            dirty.add(name);
            continue;
        }
        if (state.localOnlyConfigs.includes(name)) {
            dirty.add(name);
            continue;
        }
        const serverJson = JSON.stringify(state.server[name] ?? []);
        const draftJson = JSON.stringify(state.draft[name] ?? []);
        if (serverJson !== draftJson) {
            dirty.add(name);
        }
    }
    return dirty;
};

export const forwardDraftReducer = (
    state: ForwardDraftState,
    action: ForwardDraftAction,
): ForwardDraftState => {
    switch (action.type) {
        case 'LOAD_SERVER': {
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                server: { ...state.server, [action.configName]: action.rules },
                draft: { ...state.draft, [action.configName]: action.rules },
                serverConfigs: state.serverConfigs.includes(action.configName)
                    ? state.serverConfigs
                    : [...state.serverConfigs, action.configName],
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'LOAD_ALL_CONFIGS': {
            const newServer: Record<string, Rule[]> = { ...state.server };
            const newDraft: Record<string, Rule[]> = { ...state.draft };
            const serverConfigs: string[] = [];
            for (const { name, rules } of action.configs) {
                newServer[name] = rules;
                // Only overwrite draft if config is not locally dirty.
                const currentDirtyServer = JSON.stringify(state.server[name] ?? []);
                const currentDirtyDraft = JSON.stringify(state.draft[name] ?? []);
                if (currentDirtyServer === currentDirtyDraft) {
                    newDraft[name] = rules;
                }
                serverConfigs.push(name);
            }
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                server: newServer,
                draft: newDraft,
                serverConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'ADD_RULE': {
            const current = state.draft[action.configName] ?? [];
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: [...current, action.rule] },
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'UPDATE_RULE_AT_INDEX': {
            const current = state.draft[action.configName] ?? [];
            const updated = [...current];
            updated[action.index] = action.rule;
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: updated },
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REMOVE_RULES': {
            const current = state.draft[action.configName] ?? [];
            const toRemove = new Set(action.indices);
            const updated = current.filter((_, idx) => !toRemove.has(idx));
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: updated },
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'DUPLICATE_RULE': {
            const current = state.draft[action.configName] ?? [];
            if (action.index < 0 || action.index >= current.length) {
                return state;
            }
            const rule = current[action.index];
            const updated = [...current];
            updated.splice(action.index + 1, 0, rule);
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: updated },
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REPLACE_ALL_RULES': {
            const isNew = !state.serverConfigs.includes(action.configName)
                && !state.localOnlyConfigs.includes(action.configName);
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: action.rules },
                localOnlyConfigs: isNew
                    ? [...state.localOnlyConfigs, action.configName]
                    : state.localOnlyConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'ADD_CONFIG': {
            if (
                state.serverConfigs.includes(action.configName)
                || state.localOnlyConfigs.includes(action.configName)
            ) {
                return state;
            }
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: [] },
                localOnlyConfigs: [...state.localOnlyConfigs, action.configName],
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'DELETE_CONFIG': {
            const isLocalOnly = state.localOnlyConfigs.includes(action.configName);
            if (isLocalOnly) {
                // Just remove from local state — no server record.
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const next: Omit<ForwardDraftState, 'dirty'> = {
                    ...state,
                    draft: draftRest,
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                };
                return { ...next, dirty: recomputeDirty(next) };
            }
            // Server config: mark as pending delete — actual deletion happens on save.
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.add(action.configName);
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                pendingDeleteConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'DISCARD_CONFIG': {
            const serverRules = state.server[action.configName];
            if (serverRules === undefined) {
                // Local-only: just clear the draft back to empty.
                const next: Omit<ForwardDraftState, 'dirty'> = {
                    ...state,
                    draft: { ...state.draft, [action.configName]: [] },
                    pendingDeleteConfigs: (() => {
                        const s = new Set(state.pendingDeleteConfigs);
                        s.delete(action.configName);
                        return s;
                    })(),
                };
                return { ...next, dirty: recomputeDirty(next) };
            }
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);
            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                draft: { ...state.draft, [action.configName]: serverRules },
                pendingDeleteConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'MARK_SAVED': {
            const draftRules = state.draft[action.configName];
            const pendingDeleteConfigs = new Set(state.pendingDeleteConfigs);
            pendingDeleteConfigs.delete(action.configName);

            if (draftRules === undefined) {
                // Config was deleted.
                const { [action.configName]: _s, ...serverRest } = state.server;
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const next: Omit<ForwardDraftState, 'dirty'> = {
                    ...state,
                    server: serverRest,
                    draft: draftRest,
                    serverConfigs: state.serverConfigs.filter(n => n !== action.configName),
                    localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                    pendingDeleteConfigs,
                };
                return { ...next, dirty: recomputeDirty(next) };
            }

            const next: Omit<ForwardDraftState, 'dirty'> = {
                ...state,
                server: { ...state.server, [action.configName]: draftRules },
                serverConfigs: state.serverConfigs.includes(action.configName)
                    ? state.serverConfigs
                    : [...state.serverConfigs, action.configName],
                localOnlyConfigs: state.localOnlyConfigs.filter(n => n !== action.configName),
                pendingDeleteConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        default:
            return state;
    }
};
