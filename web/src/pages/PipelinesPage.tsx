import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { toaster } from '../utils';
import { API } from '../api';
import type { Pipeline, PipelineId } from '../api/pipelines';
import { PageLayout, PageLoader, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import { InstanceContent, CreatePipelineDialog } from './pipelines';

const PipelinesPage = (): React.JSX.Element => {
    const [initialLoading, setInitialLoading] = useState<boolean>(true);
    const [pipelinesLoading, setPipelinesLoading] = useState<boolean>(true);
    const [pipelines, setPipelines] = useState<Pipeline[]>([]);
    const [instances, setInstances] = useState<number[]>([]);
    const [createDialogOpen, setCreateDialogOpen] = useState(false);

    const { activeTab, setActiveTab, currentTabIndex } = useInstanceTabs({ items: instances });
    const selectedInstance = instances[currentTabIndex] ?? 0;

    // Load available instances
    useEffect(() => {
        let isMounted = true;

        const loadInstances = async (): Promise<void> => {
            try {
                const data = await API.inspect.inspect();
                if (!isMounted) return;

                const instanceIndices = data.instanceIndices || [];
                setInstances(instanceIndices);
            } catch (err) {
                if (!isMounted) return;
                toaster.error('instances-error', 'Failed to fetch instances', err);
            } finally {
                if (isMounted) {
                    setInitialLoading(false);
                }
            }
        };

        loadInstances();

        return () => {
            isMounted = false;
        };
    }, []);

    // Load pipelines for selected instance
    const loadPipelines = useCallback(async () => {
        if (instances.length === 0) return;

        setPipelinesLoading(true);

        try {
            const listResponse = await API.pipelines.list({ instance: selectedInstance });
            const pipelineIds: PipelineId[] = listResponse.ids || [];

            const pipelinesData: Pipeline[] = [];
            for (const pipelineId of pipelineIds) {
                try {
                    const getResponse = await API.pipelines.get({
                        instance: selectedInstance,
                        id: pipelineId,
                    });
                    if (getResponse.pipeline) {
                        pipelinesData.push(getResponse.pipeline);
                    }
                } catch (err) {
                    console.error(`Failed to fetch pipeline ${pipelineId.name}:`, err);
                }
            }

            setPipelines(pipelinesData);
        } catch (err) {
            toaster.error('pipelines-error', 'Failed to fetch pipelines', err);
            setPipelines([]);
        } finally {
            setPipelinesLoading(false);
        }
    }, [selectedInstance, instances.length]);

    useEffect(() => {
        loadPipelines();
    }, [loadPipelines]);

    const headerContent = (
        <Box style={{ display: 'flex', width: '100%', alignItems: 'center' }}>
            <Text variant="header-1">Pipelines</Text>
            <Box style={{ flex: 1 }} />
            <Button view="action" onClick={() => setCreateDialogOpen(true)}>
                <Icon data={Plus} />
                Create Pipeline
            </Button>
        </Box>
    );

    if (initialLoading) {
        return (
            <PageLayout title="Pipelines">
                <PageLoader loading={initialLoading} size="l" />
            </PageLayout>
        );
    }

    if (instances.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box
                    style={{
                        width: '100%',
                        flex: 1,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        padding: '20px',
                    }}
                >
                    <Text variant="body-1" color="secondary">
                        No dataplane instances found
                    </Text>
                </Box>
                <CreatePipelineDialog
                    open={createDialogOpen}
                    onClose={() => setCreateDialogOpen(false)}
                    onCreated={loadPipelines}
                    instance={selectedInstance}
                />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                <InstanceTabs
                    items={instances}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(instanceIdx) => `Instance ${instanceIdx}`}
                    renderContent={(instanceIdx) => (
                        <InstanceContent
                            instance={instanceIdx}
                            pipelines={pipelines}
                            loading={pipelinesLoading}
                            onRefresh={loadPipelines}
                        />
                    )}
                    contentStyle={{ flex: 1, overflow: 'auto' }}
                />
            </Box>
            <CreatePipelineDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onCreated={loadPipelines}
                instance={selectedInstance}
            />
        </PageLayout>
    );
};

export default PipelinesPage;
