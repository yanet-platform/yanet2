import React, { useState, useCallback } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    PipelinePageHeader,
    PipelineCard,
    CreatePipelineDialog,
    usePipelineData,
} from './pipelines';

const PipelinesPage: React.FC = () => {
    const {
        pipelineIds,
        loading,
        loadPipeline,
        createPipeline,
        updatePipeline,
        deletePipeline,
        loadFunctionList,
    } = usePipelineData();

    const [createDialogOpen, setCreateDialogOpen] = useState(false);

    const handleCreatePipeline = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback(async (name: string) => {
        const success = await createPipeline(name);
        if (success) {
            setCreateDialogOpen(false);
        }
    }, [createPipeline]);

    const headerContent = (
        <PipelinePageHeader onCreatePipeline={handleCreatePipeline} />
    );

    if (loading) {
        return (
            <PageLayout title="Pipelines">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box style={{
                width: '100%',
                flex: 1,
                minWidth: 0,
                padding: '20px',
                display: 'flex',
                flexDirection: 'column',
                overflow: 'hidden',
            }}>
                <Box style={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '16px',
                    overflowY: 'auto',
                    flex: 1,
                    minHeight: 0,
                }}>
                    {pipelineIds.length === 0 ? (
                        <EmptyState message="No pipelines found. Click 'Create pipeline' to add one." />
                    ) : (
                        pipelineIds.map((pipelineId) => (
                            <PipelineCard
                                key={pipelineId.name}
                                pipelineId={pipelineId}
                                loadPipeline={loadPipeline}
                                updatePipeline={updatePipeline}
                                deletePipeline={deletePipeline}
                                loadFunctionList={loadFunctionList}
                            />
                        ))
                    )}
                </Box>
            </Box>

            <CreatePipelineDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={handleCreateConfirm}
            />
        </PageLayout>
    );
};

export default PipelinesPage;
