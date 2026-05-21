import type { FIBRowItem } from './types';

export interface FIBDraftState {
    /** Server-authoritative rows, keyed by config name. */
    server: Record<string, FIBRowItem[]>;
    /** Local draft rows, keyed by config name. */
    draft: Record<string, FIBRowItem[]>;
    /** Config names known on the server (loaded on init). */
    serverConfigs: string[];
    /** Config names created locally but not yet committed. */
    localOnlyConfigs: string[];
    /** Which config names have uncommitted edits. */
    dirty: Set<string>;
}

export const initialFIBDraftState: FIBDraftState = {
    server: {},
    draft: {},
    serverConfigs: [],
    localOnlyConfigs: [],
    dirty: new Set(),
};

export type FIBDraftAction =
    | { type: 'LOAD_ALL_CONFIGS'; configs: Array<{ name: string; rows: FIBRowItem[] }> }
    | { type: 'ADD_ROW'; configName: string; row: FIBRowItem; afterIndex?: number }
    | { type: 'UPDATE_ROW'; configName: string; id: string; patch: Partial<FIBRowItem> }
    | { type: 'REMOVE_ROW'; configName: string; id: string }
    | { type: 'REMOVE_ROWS'; configName: string; ids: string[] }
    | { type: 'REORDER_ROWS'; configName: string; rows: FIBRowItem[] }
    | { type: 'REPLACE_ALL_ROWS'; configName: string; rows: FIBRowItem[] }
    | { type: 'ADD_CONFIG'; configName: string }
    | { type: 'DELETE_CONFIG'; configName: string }
    | { type: 'DISCARD_CONFIG'; configName: string }
    | { type: 'MARK_COMMITTED'; configName: string };

const recomputeDirty = (state: Omit<FIBDraftState, 'dirty'>): Set<string> => {
    const dirty = new Set<string>();
    const allConfigs = new Set([
        ...state.serverConfigs,
        ...state.localOnlyConfigs,
    ]);
    for (const name of allConfigs) {
        if (state.localOnlyConfigs.includes(name)) {
            dirty.add(name);
            continue;
        }
        const serverRows = state.server[name] ?? [];
        const draftRows = state.draft[name] ?? [];
        const serverNorm = JSON.stringify(serverRows.map((r) => ({ prefix: r.prefix, dst_mac: r.dst_mac, src_mac: r.src_mac, device: r.device })));
        const draftNorm = JSON.stringify(draftRows.map((r) => ({ prefix: r.prefix, dst_mac: r.dst_mac, src_mac: r.src_mac, device: r.device })));
        if (serverNorm !== draftNorm) {
            dirty.add(name);
        }
    }
    return dirty;
};

export const fibDraftReducer = (
    state: FIBDraftState,
    action: FIBDraftAction,
): FIBDraftState => {
    switch (action.type) {
        case 'LOAD_ALL_CONFIGS': {
            const newServer: Record<string, FIBRowItem[]> = { ...state.server };
            const newDraft: Record<string, FIBRowItem[]> = { ...state.draft };
            const serverConfigs: string[] = [];
            for (const { name, rows } of action.configs) {
                newServer[name] = rows;
                const prevServerNorm = JSON.stringify((state.server[name] ?? []).map((r) => ({ prefix: r.prefix, dst_mac: r.dst_mac, src_mac: r.src_mac, device: r.device })));
                const prevDraftNorm = JSON.stringify((state.draft[name] ?? []).map((r) => ({ prefix: r.prefix, dst_mac: r.dst_mac, src_mac: r.src_mac, device: r.device })));
                if (prevServerNorm === prevDraftNorm) {
                    newDraft[name] = rows;
                }
                serverConfigs.push(name);
            }
            const next = { ...state, server: newServer, draft: newDraft, serverConfigs };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'ADD_ROW': {
            const current = state.draft[action.configName] ?? [];
            const updated = [...current];
            const insertIdx = action.afterIndex == null ? updated.length : action.afterIndex + 1;
            updated.splice(insertIdx, 0, action.row);
            const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'UPDATE_ROW': {
            const current = state.draft[action.configName] ?? [];
            const updated = current.map((r) => r.id === action.id ? { ...r, ...action.patch } : r);
            const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REMOVE_ROW': {
            const current = state.draft[action.configName] ?? [];
            const updated = current.filter((r) => r.id !== action.id);
            const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REMOVE_ROWS': {
            const idSet = new Set(action.ids);
            const current = state.draft[action.configName] ?? [];
            const updated = current.filter((r) => !idSet.has(r.id));
            const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REORDER_ROWS': {
            const next = { ...state, draft: { ...state.draft, [action.configName]: action.rows } };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'REPLACE_ALL_ROWS': {
            const isNew = !state.serverConfigs.includes(action.configName)
                && !state.localOnlyConfigs.includes(action.configName);
            const next = {
                ...state,
                draft: { ...state.draft, [action.configName]: action.rows },
                localOnlyConfigs: isNew
                    ? [...state.localOnlyConfigs, action.configName]
                    : state.localOnlyConfigs,
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'ADD_CONFIG': {
            if (state.serverConfigs.includes(action.configName)
                || state.localOnlyConfigs.includes(action.configName)) {
                return state;
            }
            const next = {
                ...state,
                draft: { ...state.draft, [action.configName]: [] },
                localOnlyConfigs: [...state.localOnlyConfigs, action.configName],
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'DELETE_CONFIG': {
            const isLocalOnly = state.localOnlyConfigs.includes(action.configName);
            if (isLocalOnly) {
                const { [action.configName]: _d, ...draftRest } = state.draft;
                const next = {
                    ...state,
                    draft: draftRest,
                    localOnlyConfigs: state.localOnlyConfigs.filter((n) => n !== action.configName),
                };
                return { ...next, dirty: recomputeDirty(next) };
            }
            // For server configs: delete means remove from local draft (will not call server delete).
            // The commit action handles the actual API call.
            const { [action.configName]: _s, ...serverRest } = state.server;
            const { [action.configName]: _dr, ...draftRest2 } = state.draft;
            const next = {
                ...state,
                server: serverRest,
                draft: draftRest2,
                serverConfigs: state.serverConfigs.filter((n) => n !== action.configName),
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'DISCARD_CONFIG': {
            const serverRows = state.server[action.configName];
            if (serverRows === undefined) {
                const next = {
                    ...state,
                    draft: { ...state.draft, [action.configName]: [] },
                };
                return { ...next, dirty: recomputeDirty(next) };
            }
            const next = {
                ...state,
                draft: { ...state.draft, [action.configName]: serverRows },
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        case 'MARK_COMMITTED': {
            const draftRows = state.draft[action.configName] ?? [];
            const next = {
                ...state,
                server: { ...state.server, [action.configName]: draftRows },
                serverConfigs: state.serverConfigs.includes(action.configName)
                    ? state.serverConfigs
                    : [...state.serverConfigs, action.configName],
                localOnlyConfigs: state.localOnlyConfigs.filter((n) => n !== action.configName),
            };
            return { ...next, dirty: recomputeDirty(next) };
        }

        default:
            return state;
    }
};
