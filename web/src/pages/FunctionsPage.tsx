import React, { useState, useCallback } from 'react';
import { Box, Alert } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import {
    FunctionPageHeader,
    FunctionCard,
    CreateFunctionDialog,
    useFunctionData,
} from './functions';
import './FunctionsPage.css';

const FunctionsPage: React.FC = () => {
    const {
        instances,
        loading,
        error,
        functionsByInstance,
        loadFunction,
        createFunction,
        updateFunction,
        deleteFunction,
    } = useFunctionData();
    
    const { activeTab, setActiveTab, currentTabIndex } = useInstanceTabs({ items: instances });
    
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    
    const currentInstance = instances[currentTabIndex];
    const currentInstanceNumber = currentInstance?.instance ?? currentTabIndex;
    
    const handleCreateFunction = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);
    
    const handleCreateConfirm = useCallback(async (name: string) => {
        const success = await createFunction(currentInstanceNumber, name);
        if (success) {
            setCreateDialogOpen(false);
        }
    }, [createFunction, currentInstanceNumber]);
    
    const headerContent = (
        <FunctionPageHeader onCreateFunction={handleCreateFunction} />
    );
    
    if (loading) {
        return (
            <PageLayout title="Functions">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }
    
    if (instances.length === 0) {
        return (
            <PageLayout header={headerContent}>
                {error && (
                    <Box style={{ padding: '12px 20px' }}>
                        <Alert theme="danger" message={error} />
                    </Box>
                )}
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <EmptyState message="No instances found." />
                </Box>
                
                <CreateFunctionDialog
                    open={createDialogOpen}
                    onClose={() => setCreateDialogOpen(false)}
                    onConfirm={handleCreateConfirm}
                />
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
                {error && (
                    <Box style={{ marginBottom: '12px' }}>
                        <Alert theme="danger" message={error} />
                    </Box>
                )}
                <InstanceTabs
                    items={instances}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(inst) => `Instance ${inst.instance}`}
                    renderContent={(inst) => (
                        <Box style={{ 
                            display: 'flex', 
                            flexDirection: 'column', 
                            gap: '16px',
                            overflowY: 'auto',
                            flex: 1,
                            minHeight: 0,
                        }}>
                            {inst.functionIds.length === 0 ? (
                                <EmptyState message="No functions in this instance. Click 'Create function' to add one." />
                            ) : (
                                inst.functionIds.map((funcId) => (
                                    <FunctionCard
                                        key={funcId.name}
                                        instance={inst.instance}
                                        functionId={funcId}
                                        initialFunction={functionsByInstance[inst.instance]?.[funcId.name || '']}
                                        loadFunction={loadFunction}
                                        updateFunction={updateFunction}
                                        deleteFunction={deleteFunction}
                                    />
                                ))
                            )}
                        </Box>
                    )}
                    contentStyle={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}
                />
            </Box>
            
            <CreateFunctionDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={handleCreateConfirm}
            />
        </PageLayout>
    );
};

export default FunctionsPage;

