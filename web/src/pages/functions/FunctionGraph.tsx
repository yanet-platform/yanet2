import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, Button, Icon } from '@gravity-ui/uikit';
import { TrashBin, FloppyDisk } from '@gravity-ui/icons';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { API } from '../../api';
import type { Function as NetworkFunction, ModuleId, FunctionChain } from '../../api/functions';
import { GraphView, GraphViewHandle, ChainPath, ChainsResult } from './Graph';
import { ModuleEditDialog } from './ModuleEditDialog';
import { DeleteFunctionDialog } from './DeleteFunctionDialog';
import { ChainSettingsDialog } from './ChainSettingsDialog';

export interface FunctionGraphProps {
    functionData: NetworkFunction;
    instance: number;
    onDeleted: () => void;
    onSaved: () => void;
}

export const FunctionGraph: React.FC<FunctionGraphProps> = ({
    functionData,
    instance,
    onDeleted,
    onSaved,
}) => {
    const functionName = functionData.id?.name || 'Unknown';
    const [deleting, setDeleting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [isChainsValid, setIsChainsValid] = useState(true);
    const [orphanedBlocks, setOrphanedBlocks] = useState<string[]>([]);
    const [graphChains, setGraphChains] = useState<ChainPath[]>([]);
    const [lastSavedChains, setLastSavedChains] = useState<ChainPath[]>([]);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [chainSettingsOpen, setChainSettingsOpen] = useState(false);
    const [editingBlockId, setEditingBlockId] = useState<string | null>(null);
    const [editingModuleId, setEditingModuleId] = useState<ModuleId | undefined>(undefined);
    const graphRef = useRef<GraphViewHandle>(null);

    // Convert API chains to ChainPath format
    const initialChains = useMemo<ChainPath[]>(() => {
        const apiChains = functionData.chains || [];
        return apiChains.map((fc: FunctionChain) => ({
            modules: fc.chain?.modules || [],
            weight: typeof fc.weight === 'string' ? parseInt(fc.weight, 10) || 1 : fc.weight || 1,
        }));
    }, [functionData.chains]);

    useEffect(() => {
        setGraphChains(initialChains);
        setLastSavedChains(initialChains);
    }, [initialChains]);

    const areChainsEqual = useCallback((a: ChainPath[], b: ChainPath[]) => {
        if (a.length !== b.length) return false;
        return a.every((chain, chainIndex) => {
            const other = b[chainIndex];
            if (!other) return false;
            if (chain.weight !== other.weight) return false;
            if (chain.modules.length !== other.modules.length) return false;
            return chain.modules.every((module, moduleIndex) => {
                const otherModule = other.modules[moduleIndex];
                return module.name === otherModule?.name && module.type === otherModule?.type;
            });
        });
    }, []);

    const isDirty = useMemo(
        () => !areChainsEqual(graphChains, lastSavedChains),
        [areChainsEqual, graphChains, lastSavedChains]
    );

    const handleDelete = async () => {
        setDeleting(true);
        try {
            await API.functions.delete({ instance, id: { name: functionName } });
            toaster.add({
                name: 'function-deleted',
                title: 'Success',
                content: `Function "${functionName}" deleted`,
                theme: 'success',
                isClosable: true,
                autoHiding: 3000,
            });
            onDeleted();
            setDeleteDialogOpen(false);
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'delete-error',
                title: 'Error',
                content: `Failed to delete function: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
        } finally {
            setDeleting(false);
        }
    };

    const handleSave = async () => {
        if (!graphRef.current) return;

        const result = graphRef.current.getChains();
        if (!result.isValid) {
            const message = result.orphanedBlocks.length > 0
                ? `Some blocks are not connected: ${result.orphanedBlocks.length} orphaned block(s)`
                : 'All chains must connect from Input to Output';
            toaster.add({
                name: 'invalid-chain',
                title: 'Invalid Configuration',
                content: message,
                theme: 'warning',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }

        setSaving(true);
        try {
            // Convert ChainPath[] to API format
            const apiChains: FunctionChain[] = result.chains.map((chain, index) => ({
                chain: {
                    name: functionData.chains?.[index]?.chain?.name || `chain-${index}`,
                    modules: chain.modules,
                },
                weight: String(chain.weight),
            }));

            await API.functions.update({
                instance,
                function: {
                    id: { name: functionName },
                    chains: apiChains,
                },
            });

            toaster.add({
                name: 'function-saved',
                title: 'Success',
                content: `Function "${functionName}" saved with ${result.chains.length} chain(s)`,
                theme: 'success',
                isClosable: true,
                autoHiding: 3000,
            });
            setLastSavedChains(result.chains);
            setGraphChains(result.chains);
            onSaved();
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'save-error',
                title: 'Error',
                content: `Failed to save function: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
        } finally {
            setSaving(false);
        }
    };

    const handleChainsChange = useCallback((result: ChainsResult) => {
        setIsChainsValid(result.isValid);
        setOrphanedBlocks(result.orphanedBlocks);
        setGraphChains(result.chains);
    }, []);

    const handleModuleEdit = useCallback((blockId: string, moduleId: ModuleId | undefined) => {
        setEditingBlockId(blockId);
        setEditingModuleId(moduleId);
    }, []);

    const handleModuleSave = useCallback((moduleId: ModuleId) => {
        if (editingBlockId && graphRef.current) {
            graphRef.current.updateModule(editingBlockId, moduleId);
        }
    }, [editingBlockId]);

    const handleInputBlockEdit = useCallback(() => {
        setChainSettingsOpen(true);
    }, []);

    const handleChainSettingsSave = useCallback((updatedChains: ChainPath[]) => {
        // Update weights in the graph
        if (graphRef.current) {
            updatedChains.forEach((chain, index) => {
                graphRef.current?.updateChainWeight(index, chain.weight);
            });
        }
        setGraphChains(updatedChains);
    }, []);

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
                        {functionName}
                    </Text>
                    <Text variant="body-1" color="secondary">
                        {graphChains.length} chain{graphChains.length !== 1 ? 's' : ''}
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
                        disabled={!isChainsValid || !isDirty}
                        title={
                            !isChainsValid
                                ? orphanedBlocks.length > 0
                                    ? 'All blocks must be connected'
                                    : 'All chains must connect Input → ... → Output'
                                : isDirty
                                    ? 'Save function'
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
                        title="Delete function"
                    >
                        <Icon data={TrashBin} />
                    </Button>
                </Box>
            </Box>
            <Box style={{ padding: '12px' }}>
                <GraphView
                    ref={graphRef}
                    functionId={functionName}
                    initialChains={initialChains}
                    height={500}
                    onChainsChange={handleChainsChange}
                    onModuleEdit={handleModuleEdit}
                    onInputBlockEdit={handleInputBlockEdit}
                />
            </Box>

            <ModuleEditDialog
                open={editingBlockId !== null}
                onClose={() => setEditingBlockId(null)}
                moduleId={editingModuleId}
                onSave={handleModuleSave}
            />
            <DeleteFunctionDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDelete}
                functionName={functionName}
                loading={deleting}
            />
            <ChainSettingsDialog
                open={chainSettingsOpen}
                onClose={() => setChainSettingsOpen(false)}
                chains={graphChains}
                onSave={handleChainSettingsSave}
            />
        </Box>
    );
};

