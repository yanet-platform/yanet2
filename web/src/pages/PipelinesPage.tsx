import React, { useState, useCallback } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    PipelinePageHeader,
    PipelineCard,
    CreatePipelineDialog,
    usePipelineData,
} from './pipelines';
import './pipelines/pipelines.css';

const PipelinesPage: React.FC = () => {
    const {
        pipelineIds,
        pipelines,
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
            <Box className="pipelines-page__content">
                <Box className="pipelines-page__list">
                    {pipelineIds.length === 0 ? (
                        <EmptyState message="No pipelines found. Click 'Create pipeline' to add one." />
                    ) : (
                        pipelineIds.map((pipelineId) => (
                            <PipelineCard
                                key={pipelineId.name}
                                pipelineId={pipelineId}
                                initialPipeline={pipelines[pipelineId.name || '']}
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
