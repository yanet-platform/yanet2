import React, { useState, useCallback, useMemo } from 'react';
import type { Pipeline, PipelinesAction, DragPayload, FunctionRef } from '../types';
import { useFunctionRefCounters, type FunctionRefInfo, PIPELINE_COUNTER_KEY } from '../hooks/useFunctionRefCounters';
import { getDragPayload, useSparklineHistory } from '../../_shared/lane-editor';
import { PipelineCardHeader } from './PipelineCardHeader';
import { LaneTrack } from './LaneTrack';
import { Drawer } from './Drawer';
import { DiffModal } from './DiffModal';
import type { InterpolatedCounterData } from '../../../../hooks';
import type { FunctionId } from '../../../../api/pipelines';
import type { DragState } from '../../_shared/lane-editor';

interface PipelineCardProps {
    pipeline: Pipeline;
    serverPipeline: Pipeline | null;
    isDirty: boolean;
    dispatch: (action: PipelinesAction) => void;
    dragState: DragState;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onSave: () => Promise<void>;
    onDiscard: () => void;
    onDelete: () => Promise<boolean>;
    loadFunctionList: () => Promise<FunctionId[]>;
}

/**
 * A full pipeline card with collapsible track, drag-and-drop, drawer, and per-pipeline save.
 */
export const PipelineCard: React.FC<PipelineCardProps> = ({
    pipeline,
    serverPipeline,
    isDirty,
    dispatch,
    dragState,
    onDragStart,
    onDragEnd,
    onSave,
    onDiscard,
    onDelete,
    loadFunctionList,
}) => {
    const [collapsed, setCollapsed] = useState(false);
    const [diffOpen, setDiffOpen] = useState(false);
    const [drawerRefId, setDrawerRefId] = useState<string | null>(null);

    const refInfoList: FunctionRefInfo[] = useMemo(() =>
        pipeline.functions.map(r => ({ nodeId: r.id, functionName: r.name })),
        [pipeline.functions],
    );

    const { counters } = useFunctionRefCounters(pipeline.id, refInfoList);

    const counterMap: Map<string, InterpolatedCounterData> = useMemo(() => {
        const map = new Map<string, InterpolatedCounterData>();
        for (const [key, val] of counters.entries()) {
            map.set(key, val);
        }
        return map;
    }, [counters]);

    const totalPps = useMemo(() => {
        if (pipeline.functions.length === 0) {
            return counterMap.get(PIPELINE_COUNTER_KEY)?.pps ?? 0;
        }
        const first = pipeline.functions[0];
        return counterMap.get(first.id)?.pps ?? 0;
    }, [counterMap, pipeline.functions]);

    const sparklineData = useSparklineHistory(`pl:${pipeline.id}:total`, totalPps);

    const handleDrop = useCallback((toIdx: number): void => {
        const payload = getDragPayload();
        if (!payload) {
            return;
        }
        dispatch({
            type: 'MOVE_FUNCTION_REF',
            fromPipelineId: payload.fromFnId,
            toPipelineId: pipeline.id,
            refId: payload.moduleId,
            toIdx,
        });
        onDragEnd();
    }, [pipeline.id, dispatch, onDragEnd]);

    const handleOpenDrawer = useCallback((refId: string): void => {
        setDrawerRefId(refId);
    }, []);

    const handleCloseDrawer = useCallback((): void => {
        setDrawerRefId(null);
    }, []);

    const handleAddFunctionRef = useCallback((): void => {
        const newId = `pl:${pipeline.id}::ref:${Date.now()}::${Math.random().toString(36).slice(2)}`;
        const newRef: FunctionRef = { id: newId, name: '' };
        dispatch({
            type: 'ADD_FUNCTION_REF',
            pipelineId: pipeline.id,
            toIdx: pipeline.functions.length,
            ref: newRef,
        });
        setDrawerRefId(newId);
    }, [pipeline.id, pipeline.functions.length, dispatch]);

    const drawerRef: FunctionRef | null = useMemo(() => {
        if (!drawerRefId) {
            return null;
        }
        return pipeline.functions.find(r => r.id === drawerRefId) ?? null;
    }, [pipeline.functions, drawerRefId]);

    return (
        <div className={`pl-pipeline-card${collapsed ? ' pl-pipeline-card--collapsed' : ''}`}>
            <PipelineCardHeader
                pipeline={pipeline}
                isDirty={isDirty}
                collapsed={collapsed}
                totalPps={totalPps}
                sparklineData={sparklineData}
                onToggleCollapse={() => setCollapsed(c => !c)}
                onOpenDiff={() => setDiffOpen(true)}
                onDiscard={onDiscard}
                onDelete={onDelete}
            />

            {!collapsed && (
                <div className="pl-pipeline-card__body">
                    <div className="pl-pipeline-card__sub-header">
                        <span className="pl-pipeline-card__sub-header-item pl-pipeline-card__sub-header-item--bold">
                            {pipeline.functions.length} functions
                        </span>
                    </div>

                    <LaneTrack
                        pipelineId={pipeline.id}
                        refs={pipeline.functions}
                        dragState={dragState}
                        counterMap={counterMap}
                        onDragStart={onDragStart}
                        onDragEnd={onDragEnd}
                        onDrop={handleDrop}
                        onOpenDrawer={handleOpenDrawer}
                        onRemoveRef={refId => dispatch({ type: 'REMOVE_FUNCTION_REF', pipelineId: pipeline.id, refId })}
                        onAddRef={handleAddFunctionRef}
                    />
                </div>
            )}

            {drawerRef && (
                <Drawer
                    ref_={drawerRef}
                    counter={counterMap.get(drawerRef.id)}
                    loadFunctionList={loadFunctionList}
                    onClose={handleCloseDrawer}
                    onChangeFunction={name => {
                        dispatch({ type: 'UPDATE_FUNCTION_REF', pipelineId: pipeline.id, refId: drawerRef.id, name });
                    }}
                    onRemove={() => {
                        dispatch({ type: 'REMOVE_FUNCTION_REF', pipelineId: pipeline.id, refId: drawerRef.id });
                        handleCloseDrawer();
                    }}
                />
            )}

            {diffOpen && (
                <DiffModal
                    pipeline={pipeline}
                    serverPipeline={serverPipeline}
                    onClose={() => setDiffOpen(false)}
                    onApply={onSave}
                />
            )}
        </div>
    );
};
