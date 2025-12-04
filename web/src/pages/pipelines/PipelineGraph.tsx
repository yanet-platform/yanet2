import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, Button, Icon } from '@gravity-ui/uikit';
import { TrashBin, FloppyDisk } from '@gravity-ui/icons';
import { API } from '../../api';
import { toaster } from '../../utils';
import type { Pipeline, FunctionId } from '../../api/pipelines';
import { PipelineGraphView, PipelineGraphViewHandle, PipelineResult } from './PipelineGraphView';
import { FunctionSelectDialog } from './FunctionSelectDialog';
import { DeletePipelineDialog } from './DeletePipelineDialog';

export interface PipelineGraphProps {
    pipelineData: Pipeline;
    instance: number;
    onDeleted: () => void;
    onSaved: () => void;
}

export const PipelineGraph: React.FC<PipelineGraphProps> = ({
    pipelineData,
    instance,
    onDeleted,
    onSaved,
}) => {
    const pipelineName = pipelineData.id?.name || 'Unknown';
    const [deleting, setDeleting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [isPipelineValid, setIsPipelineValid] = useState(true);
    const [orphanedBlocks, setOrphanedBlocks] = useState<string[]>([]);
    const [graphFunctions, setGraphFunctions] = useState<FunctionId[]>([]);
    const [lastSavedFunctions, setLastSavedFunctions] = useState<FunctionId[]>([]);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [editingBlockId, setEditingBlockId] = useState<string | null>(null);
    const [editingFunctionId, setEditingFunctionId] = useState<FunctionId | undefined>(undefined);
    const graphRef = useRef<PipelineGraphViewHandle>(null);

    // Get initial functions from pipeline data
    const initialFunctions = useMemo<FunctionId[]>(() => {
        return pipelineData.functions || [];
    }, [pipelineData.functions]);

    useEffect(() => {
        setGraphFunctions(initialFunctions);
        setLastSavedFunctions(initialFunctions);
    }, [initialFunctions]);

    const areFunctionsEqual = useCallback((a: FunctionId[], b: FunctionId[]) => {
        if (a.length !== b.length) return false;
        return a.every((func, index) => {
            const other = b[index];
            return func.name === other?.name;
        });
    }, []);

    const isDirty = useMemo(
        () => !areFunctionsEqual(graphFunctions, lastSavedFunctions),
        [areFunctionsEqual, graphFunctions, lastSavedFunctions]
    );

    const handleDelete = async () => {
        setDeleting(true);
        try {
            await API.pipelines.delete({ instance, id: { name: pipelineName } });
            toaster.success('pipeline-deleted', `Pipeline "${pipelineName}" deleted`);
            onDeleted();
            setDeleteDialogOpen(false);
        } catch (err) {
            toaster.error('delete-error', 'Failed to delete pipeline', err);
        } finally {
            setDeleting(false);
        }
    };

    const handleSave = async () => {
        if (!graphRef.current) return;

        const result = graphRef.current.getPipeline();
        if (!result.isValid) {
            const message = result.orphanedBlocks.length > 0
                ? `Some blocks are not connected: ${result.orphanedBlocks.length} orphaned block(s)`
                : 'Pipeline must connect from Input to Output';
            toaster.warning('invalid-pipeline', message, 'Invalid Configuration');
            return;
        }

        setSaving(true);
        try {
            await API.pipelines.update({
                instance,
                pipeline: {
                    id: { name: pipelineName },
                    functions: result.functions,
                },
            });

            toaster.success('pipeline-saved', `Pipeline "${pipelineName}" saved with ${result.functions.length} function(s)`);
            setLastSavedFunctions(result.functions);
            setGraphFunctions(result.functions);
            onSaved();
        } catch (err) {
            toaster.error('save-error', 'Failed to save pipeline', err);
        } finally {
            setSaving(false);
        }
    };

    const handlePipelineChange = useCallback((result: PipelineResult) => {
        setIsPipelineValid(result.isValid);
        setOrphanedBlocks(result.orphanedBlocks);
        setGraphFunctions(result.functions);
    }, []);

    const handleFunctionEdit = useCallback((blockId: string, functionId: FunctionId | undefined) => {
        setEditingBlockId(blockId);
        setEditingFunctionId(functionId);
    }, []);

    const handleFunctionSave = useCallback((functionId: FunctionId) => {
        if (editingBlockId && graphRef.current) {
            graphRef.current.updateFunction(editingBlockId, functionId);
        }
    }, [editingBlockId]);

    return (
        <Box
            style={{
                marginBottom: '24px',
                border: '1px solid var(--g-color-line-generic)',
                borderRadius: '12px',
                overflow: 'hidden',
                backgroundColor: 'var(--g-color-base-background)',
            }}
        >
            <Box
                style={{
                    padding: '16px 20px',
                    backgroundColor: 'var(--g-color-base-simple-hover)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                }}
            >
                <Box style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                    <Text variant="header-1" style={{ fontWeight: 600 }}>
                        {pipelineName}
                    </Text>
                    <Text variant="body-1" color="secondary">
                        {graphFunctions.length} function{graphFunctions.length !== 1 ? 's' : ''}
                    </Text>
                    {orphanedBlocks.length > 0 && (
                        <Text variant="body-1" color="danger">
                            ({orphanedBlocks.length} orphaned block{orphanedBlocks.length !== 1 ? 's' : ''})
                        </Text>
                    )}
                </Box>
                <Box style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <Button
                        view="action"
                        size="s"
                        onClick={handleSave}
                        loading={saving}
                        disabled={!isPipelineValid || !isDirty}
                        title={
                            !isPipelineValid
                                ? orphanedBlocks.length > 0
                                    ? 'All blocks must be connected'
                                    : 'Pipeline must connect Input → ... → Output'
                                : isDirty
                                    ? 'Save pipeline'
                                    : 'No changes to save'
                        }
                    >
                        <Icon data={FloppyDisk} />
                        Save
                    </Button>
                    <Button
                        view="flat-danger"
                        size="s"
                        onClick={() => setDeleteDialogOpen(true)}
                        loading={deleting}
                        title="Delete pipeline"
                    >
                        <Icon data={TrashBin} />
                    </Button>
                </Box>
            </Box>
            <Box style={{ padding: '12px' }}>
                <PipelineGraphView
                    ref={graphRef}
                    pipelineId={pipelineName}
                    initialFunctions={initialFunctions}
                    height={400}
                    onPipelineChange={handlePipelineChange}
                    onFunctionEdit={handleFunctionEdit}
                />
            </Box>

            <FunctionSelectDialog
                open={editingBlockId !== null}
                onClose={() => setEditingBlockId(null)}
                functionId={editingFunctionId}
                onSave={handleFunctionSave}
                instance={instance}
            />
            <DeletePipelineDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDelete}
                pipelineName={pipelineName}
                loading={deleting}
            />
        </Box>
    );
};
