/** Generic factory for a local-draft reducer over a list of typed rows. */

export interface DraftState<T> {
    /** Server-authoritative rows, keyed by config name. */
    server: Record<string, T[]>;
    /** Local draft rows, keyed by config name. */
    draft: Record<string, T[]>;
    /** Config names known on the server (loaded on init). */
    serverConfigs: string[];
    /** Config names created locally but not yet committed. */
    localOnlyConfigs: string[];
    /** Which config names have uncommitted edits. */
    dirty: Set<string>;
}

export type DraftAction<T> =
    | { type: 'LOAD_ALL_CONFIGS'; configs: Array<{ name: string; rows: T[] }> }
    | { type: 'ADD_ROW'; configName: string; row: T; afterIndex?: number }
    | { type: 'UPDATE_ROW'; configName: string; id: string; patch: Partial<T> }
    | { type: 'REMOVE_ROW'; configName: string; id: string }
    | { type: 'REMOVE_ROWS'; configName: string; ids: string[] }
    | { type: 'REORDER_ROWS'; configName: string; rows: T[] }
    | { type: 'REPLACE_ALL_ROWS'; configName: string; rows: T[] }
    | { type: 'ADD_CONFIG'; configName: string }
    | { type: 'DELETE_CONFIG'; configName: string }
    | { type: 'DISCARD_CONFIG'; configName: string }
    | { type: 'MARK_COMMITTED'; configName: string };

export interface CreateDraftReducerOptions<T> {
    /** Extract a stable string id from a row. */
    getId: (item: T) => string;
    /** Return true when two rows are semantically equal (ignoring id). */
    equals: (a: T, b: T) => boolean;
}

const recomputeDirty = <T>(
    state: Omit<DraftState<T>, 'dirty'>,
    equals: (a: T, b: T) => boolean,
): Set<string> => {
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
        const srv = state.server[name] ?? [];
        const drft = state.draft[name] ?? [];
        if (srv.length !== drft.length) {
            dirty.add(name);
            continue;
        }
        const diff = srv.some((row, idx) => !equals(row, drft[idx]));
        if (diff) dirty.add(name);
    }
    return dirty;
};

export const createDraftReducer = <T extends { id?: unknown }>(
    opts: CreateDraftReducerOptions<T>,
): { reducer: (state: DraftState<T>, action: DraftAction<T>) => DraftState<T>; initialState: DraftState<T> } => {
    const { getId, equals } = opts;

    const initialState: DraftState<T> = {
        server: {},
        draft: {},
        serverConfigs: [],
        localOnlyConfigs: [],
        dirty: new Set(),
    };

    const reducer = (state: DraftState<T>, action: DraftAction<T>): DraftState<T> => {
        switch (action.type) {
            case 'LOAD_ALL_CONFIGS': {
                const newServer: Record<string, T[]> = { ...state.server };
                const newDraft: Record<string, T[]> = { ...state.draft };
                const serverConfigs: string[] = [];
                for (const { name, rows } of action.configs) {
                    newServer[name] = rows;
                    const prevServer = state.server[name] ?? [];
                    const prevDraft = state.draft[name] ?? [];
                    const unchanged =
                        prevServer.length === prevDraft.length &&
                        prevServer.every((r, idx) => equals(r, prevDraft[idx]));
                    if (unchanged) {
                        newDraft[name] = rows;
                    }
                    serverConfigs.push(name);
                }
                const next = { ...state, server: newServer, draft: newDraft, serverConfigs };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'ADD_ROW': {
                const current = state.draft[action.configName] ?? [];
                const updated = [...current];
                const insertIdx = action.afterIndex == null ? updated.length : action.afterIndex + 1;
                updated.splice(insertIdx, 0, action.row);
                const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'UPDATE_ROW': {
                const current = state.draft[action.configName] ?? [];
                const updated = current.map((r) => getId(r) === action.id ? { ...r, ...action.patch } : r);
                const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'REMOVE_ROW': {
                const current = state.draft[action.configName] ?? [];
                const updated = current.filter((r) => getId(r) !== action.id);
                const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'REMOVE_ROWS': {
                const idSet = new Set(action.ids);
                const current = state.draft[action.configName] ?? [];
                const updated = current.filter((r) => !idSet.has(getId(r)));
                const next = { ...state, draft: { ...state.draft, [action.configName]: updated } };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'REORDER_ROWS': {
                const next = { ...state, draft: { ...state.draft, [action.configName]: action.rows } };
                return { ...next, dirty: recomputeDirty(next, equals) };
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
                return { ...next, dirty: recomputeDirty(next, equals) };
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
                return { ...next, dirty: recomputeDirty(next, equals) };
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
                    return { ...next, dirty: recomputeDirty(next, equals) };
                }
                const { [action.configName]: _s, ...serverRest } = state.server;
                const { [action.configName]: _dr, ...draftRest2 } = state.draft;
                const next = {
                    ...state,
                    server: serverRest,
                    draft: draftRest2,
                    serverConfigs: state.serverConfigs.filter((n) => n !== action.configName),
                };
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            case 'DISCARD_CONFIG': {
                const serverRows = state.server[action.configName];
                if (serverRows === undefined) {
                    const next = {
                        ...state,
                        draft: { ...state.draft, [action.configName]: [] },
                    };
                    return { ...next, dirty: recomputeDirty(next, equals) };
                }
                const next = {
                    ...state,
                    draft: { ...state.draft, [action.configName]: serverRows },
                };
                return { ...next, dirty: recomputeDirty(next, equals) };
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
                return { ...next, dirty: recomputeDirty(next, equals) };
            }

            default:
                return state;
        }
    };

    return { reducer, initialState };
};
