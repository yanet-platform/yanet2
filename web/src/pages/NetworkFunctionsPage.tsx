import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { API } from '../api';
import type { Function } from '../api/functions';
import type { FunctionId } from '../api';
import { PageLayout, PageLoader, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import { InstanceContent, CreateFunctionDialog } from './functions';

const NetworkFunctionsPage = (): React.JSX.Element => {
    const [initialLoading, setInitialLoading] = useState<boolean>(true);
    const [functionsLoading, setFunctionsLoading] = useState<boolean>(true);
    const [functions, setFunctions] = useState<Function[]>([]);
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
                const errorMessage = err instanceof Error ? err.message : 'Unknown error';
                toaster.add({
                    name: 'instances-error',
                    title: 'Error',
                    content: `Failed to fetch instances: ${errorMessage}`,
                    theme: 'danger',
                    isClosable: true,
                    autoHiding: 5000,
                });
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

    // Load functions for selected instance
    const loadFunctions = useCallback(async () => {
        if (instances.length === 0) return;

        setFunctionsLoading(true);

        try {
            const listResponse = await API.functions.list({ instance: selectedInstance });
            const functionIds: FunctionId[] = listResponse.ids || [];

            const functionsData: Function[] = [];
            for (const funcId of functionIds) {
                try {
                    const getResponse = await API.functions.get({
                        instance: selectedInstance,
                        id: funcId,
                    });
                    if (getResponse.function) {
                        functionsData.push(getResponse.function);
                    }
                } catch (err) {
                    console.error(`Failed to fetch function ${funcId.name}:`, err);
                }
            }

            setFunctions(functionsData);
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'functions-error',
                title: 'Error',
                content: `Failed to fetch functions: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
            setFunctions([]);
        } finally {
            setFunctionsLoading(false);
        }
    }, [selectedInstance, instances.length]);

    useEffect(() => {
        loadFunctions();
    }, [loadFunctions]);

    const headerContent = (
        <Box style={{ display: 'flex', width: '100%', alignItems: 'center' }}>
            <Text variant="header-1">Network Functions</Text>
            <Box style={{ flex: 1 }} />
            <Button view="action" onClick={() => setCreateDialogOpen(true)}>
                <Icon data={Plus} />
                Create Function
            </Button>
        </Box>
    );

    if (initialLoading) {
        return (
            <PageLayout title="Network Functions">
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
                <CreateFunctionDialog
                    open={createDialogOpen}
                    onClose={() => setCreateDialogOpen(false)}
                    onCreated={loadFunctions}
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
                            functions={functions}
                            loading={functionsLoading}
                            onRefresh={loadFunctions}
                        />
                    )}
                    contentStyle={{ flex: 1, overflow: 'auto' }}
                />
            </Box>
            <CreateFunctionDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onCreated={loadFunctions}
                instance={selectedInstance}
            />
        </PageLayout>
    );
};

export default NetworkFunctionsPage;
