import React, { useState, useCallback, useMemo } from 'react';
import type { Pipeline, PipelinesAction, DragPayload, FunctionRef } from '../types';
import { useFunctionRefCounters, type FunctionRefInfo, PIPELINE_COUNTER_KEY } from '../hooks/useFunctionRefCounters';
import { useDragState, getDragPayload, useSparklineHistory } from '../../_shared/lane-editor';
import { PipelineCardHeader } from './PipelineCardHeader';
import { LaneTrack } from './LaneTrack';
import { Drawer } from './Drawer';
import { DiffModal } from './DiffModal';
import type { InterpolatedCounterData } from '../../../../hooks';
import type { FunctionId } from '../../../../api/pipelines';

interface PipelineCardProps {
    pipeline: Pipeline;
    serverPipeline: Pipeline | null;
    isDirty: boolean;
    dispatch: (action: PipelinesAction) => void;
    onSave: () => Promise<void>;
    onDiscard: () => void;
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
    onSave,
    onDiscard,
    loadFunctionList,
}) => {
    const [collapsed, setCollapsed] = useState(false);
    const [diffOpen, setDiffOpen] = useState(false);
    const [drawerRefId, setDrawerRefId] = useState<string | null>(null);
    const { dragState, startDrag, endDrag } = useDragState();

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

    const handleDragStart = useCallback((payload: DragPayload): void => {
        startDrag(payload);
    }, [startDrag]);

    const handleDragEnd = useCallback((): void => {
        endDrag();
    }, [endDrag]);

    const handleDrop = useCallback((toIdx: number): void => {
        const payload = getDragPayload();
        if (!payload || payload.fromFnId !== pipeline.id) {
            return;
        }
        dispatch({
            type: 'MOVE_FUNCTION_REF',
            pipelineId: pipeline.id,
            refId: payload.moduleId,
            toIdx,
        });
        endDrag();
    }, [pipeline.id, dispatch, endDrag]);

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
                onDelete={() => dispatch({ type: 'REMOVE_PIPELINE', pipelineId: pipeline.id })}
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
                        onDragStart={handleDragStart}
                        onDragEnd={handleDragEnd}
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
