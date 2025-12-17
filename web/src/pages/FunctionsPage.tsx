import React, { useState, useCallback } from 'react';
import { Box, Alert } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import {
    FunctionPageHeader,
    FunctionCard,
    CreateFunctionDialog,
    useFunctionData,
} from './functions';
import './FunctionsPage.css';

const FunctionsPage: React.FC = () => {
    const {
        functionIds,
        loading,
        error,
        functions,
        loadFunction,
        createFunction,
        updateFunction,
        deleteFunction,
    } = useFunctionData();

    const [createDialogOpen, setCreateDialogOpen] = useState(false);

    const handleCreateFunction = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback(async (name: string) => {
        const success = await createFunction(name);
        if (success) {
            setCreateDialogOpen(false);
        }
    }, [createFunction]);

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
                <Box style={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '16px',
                    overflowY: 'auto',
                    flex: 1,
                    minHeight: 0,
                }}>
                    {functionIds.length === 0 ? (
                        <EmptyState message="No functions found. Click 'Create function' to add one." />
                    ) : (
                        functionIds.map((funcId) => (
                            <FunctionCard
                                key={funcId.name}
                                functionId={funcId}
                                initialFunction={functions[funcId.name || '']}
                                loadFunction={loadFunction}
                                updateFunction={updateFunction}
                                deleteFunction={deleteFunction}
                            />
                        ))
                    )}
                </Box>
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
