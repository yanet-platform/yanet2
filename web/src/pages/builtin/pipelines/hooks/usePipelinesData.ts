import { useCallback } from 'react';
import { API } from '../../../../api';
import { toaster } from '../../../../utils';
import type { Pipeline, PipelinesAction } from '../types';
import type { FunctionId } from '../../../../api/pipelines';
import { pipelinesReducer, initialState } from '../reducer';
import { apiToLocal, localToApi } from '../wire';
import { useEditableEntityStore } from '../../_shared/editableEntityStore';

export interface UsePipelinesDataResult {
    pipelines: Pipeline[];
    loading: boolean;
    isDirty: (pipelineId: string) => boolean;
    getServerPipeline: (pipelineId: string) => Pipeline | null;
    dispatch: (action: PipelinesAction) => void;
    savePipeline: (pipelineId: string) => Promise<void>;
    discardPipeline: (pipelineId: string) => void;
    createPipeline: (name: string) => Promise<boolean>;
    deletePipeline: (pipelineId: string) => Promise<boolean>;
    loadFunctionList: () => Promise<FunctionId[]>;
}

/**
 * Loads all pipelines from the API, manages local edit state via useReducer,
 * and exposes per-pipeline save/discard/create operations.
 */
export const usePipelinesData = (): UsePipelinesDataResult => {
    const store = useEditableEntityStore({
        reducer: pipelinesReducer,
        initialState,
        api: API.pipelines,
        getIds: resp => resp.ids ?? [],
        idName: id => id.name ?? '',
        getEntity: resp => resp.pipeline,
        apiToLocal,
        localToApi,
        makeUpdateRequest: pl => ({ pipeline: pl }),
        makeDeleteRequest: name => ({ id: { name } }),
        makeCreateRequest: name => ({ pipeline: { id: { name }, functions: [] } }),
        toastPrefix: 'pl',
        entityLabel: 'Pipeline',
    });

    const getServerPipeline = useCallback(
        (pipelineId: string): Pipeline | null => store.getServer(pipelineId),
        [store.getServer],
    );

    const savePipeline = useCallback(
        (pipelineId: string): Promise<void> => store.save(pipelineId),
        [store.save],
    );

    const discardPipeline = useCallback(
        (pipelineId: string): void => store.discard(pipelineId),
        [store.discard],
    );

    const isDirty = useCallback(
        (pipelineId: string): boolean => store.isDirty(pipelineId),
        [store.isDirty],
    );

    const createPipeline = useCallback(
        (name: string): Promise<boolean> => store.create(name),
        [store.create],
    );

    const deletePipeline = useCallback(
        (pipelineId: string): Promise<boolean> => store.remove(pipelineId),
        [store.remove],
    );

    const loadFunctionList = useCallback(async (): Promise<FunctionId[]> => {
        try {
            const resp = await API.functions.list({});
            return resp.ids ?? [];
        } catch (err) {
            toaster.error('pl-fn-list', 'Failed to load function list', err);
            return [];
        }
    }, []);

    return {
        pipelines: store.entities,
        loading: store.loading,
        isDirty,
        getServerPipeline,
        dispatch: store.dispatch,
        savePipeline,
        discardPipeline,
        createPipeline,
        deletePipeline,
        loadFunctionList,
    };
};
