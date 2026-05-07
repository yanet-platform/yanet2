import { useCallback, useEffect, useReducer, useState } from 'react';
import { API } from '../../../../api';
import { toaster } from '../../../../utils';
import type { Pipeline, PipelinesAction } from '../types';
import type { FunctionId } from '../../../../api/pipelines';
import { pipelinesReducer, initialState, discardEdits } from '../reducer';
import { apiToLocal, localToApi } from '../wire';

export interface UsePipelinesDataResult {
    pipelines: Pipeline[];
    loading: boolean;
    isDirty: (pipelineId: string) => boolean;
    getServerPipeline: (pipelineId: string) => Pipeline | null;
    dispatch: (action: PipelinesAction) => void;
    savePipeline: (pipelineId: string) => Promise<void>;
    discardPipeline: (pipelineId: string) => void;
    createPipeline: (name: string) => Promise<boolean>;
    loadFunctionList: () => Promise<FunctionId[]>;
}

/**
 * Loads all pipelines from the API, manages local edit state via useReducer,
 * and exposes per-pipeline save/discard/create operations.
 */
export const usePipelinesData = (): UsePipelinesDataResult => {
    const [state, rawDispatch] = useReducer(pipelinesReducer, initialState);
    const [loading, setLoading] = useState(true);
    const [pipelineIds, setPipelineIds] = useState<string[]>([]);

    const dispatch = useCallback((action: PipelinesAction) => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const listResp = await API.pipelines.list({});
            const ids = listResp.ids ?? [];
            const names = ids.map(id => id.name ?? '').filter(Boolean);
            setPipelineIds(names);

            await Promise.all(ids.map(async (pid) => {
                try {
                    const resp = await API.pipelines.get({ id: pid });
                    if (resp.pipeline) {
                        rawDispatch({ type: 'LOAD_PIPELINE', pipeline: apiToLocal(resp.pipeline) });
                    }
                } catch (err) {
                    toaster.error('pl-load', `Failed to load pipeline ${pid.name}`, err);
                }
            }));
        } catch (err) {
            toaster.error('pl-list', 'Failed to load pipelines', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const savePipeline = useCallback(async (pipelineId: string): Promise<void> => {
        const pl = state.local[pipelineId];
        if (!pl) {
            return;
        }
        try {
            await API.pipelines.update({ pipeline: localToApi(pl) });
            rawDispatch({ type: 'LOAD_PIPELINE', pipeline: pl });
            toaster.success(`pl-save-${pipelineId}`, `Pipeline "${pipelineId}" saved.`);
        } catch (err) {
            toaster.error(`pl-save-err-${pipelineId}`, `Failed to save "${pipelineId}"`, err);
        }
    }, [state.local]);

    const discardPipeline = useCallback((pipelineId: string): void => {
        const next = discardEdits(state, pipelineId);
        const reverted = next.local[pipelineId];
        if (reverted) {
            rawDispatch({ type: 'LOAD_PIPELINE', pipeline: reverted });
        }
    }, [state]);

    const isDirty = useCallback((pipelineId: string): boolean => state.dirty.has(pipelineId), [state.dirty]);

    const getServerPipeline = useCallback((pipelineId: string): Pipeline | null =>
        state.server[pipelineId] ?? null, [state.server]);

    const createPipeline = useCallback(async (name: string): Promise<boolean> => {
        try {
            await API.pipelines.update({ pipeline: { id: { name }, functions: [] } });
            await load();
            toaster.success(`pl-create-${name}`, `Pipeline "${name}" created.`);
            return true;
        } catch (err) {
            toaster.error(`pl-create-err-${name}`, `Failed to create "${name}"`, err);
            return false;
        }
    }, [load]);

    const loadFunctionList = useCallback(async (): Promise<FunctionId[]> => {
        try {
            const resp = await API.functions.list({});
            return resp.ids ?? [];
        } catch (err) {
            toaster.error('pl-fn-list', 'Failed to load function list', err);
            return [];
        }
    }, []);

    const pipelines = pipelineIds
        .map(id => state.local[id])
        .filter((p): p is Pipeline => !!p);

    return { pipelines, loading, isDirty, getServerPipeline, dispatch, savePipeline, discardPipeline, createPipeline, loadFunctionList };
};
