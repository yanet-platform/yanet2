import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Dialog, TextInput, Select, Loader } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { API } from '../../api';
import type { FunctionId } from '../../api/pipelines';

export interface FunctionSelectDialogProps {
    open: boolean;
    onClose: () => void;
    functionId: FunctionId | undefined;
    onSave: (functionId: FunctionId) => void;
    instance: number;
}

export const FunctionSelectDialog: React.FC<FunctionSelectDialogProps> = ({
    open,
    onClose,
    functionId,
    onSave,
    instance,
}) => {
    const [name, setName] = useState(functionId?.name || '');
    const [availableFunctions, setAvailableFunctions] = useState<FunctionId[]>([]);
    const [loading, setLoading] = useState(false);

    // Load available functions when dialog opens
    useEffect(() => {
        if (!open) return;

        const loadFunctions = async () => {
            setLoading(true);
            try {
                const response = await API.functions.list({ instance });
                setAvailableFunctions(response.ids || []);
            } catch (err) {
                console.error('Failed to load functions:', err);
                toaster.add({
                    name: 'load-functions-error',
                    title: 'Error',
                    content: 'Failed to load available functions',
                    theme: 'danger',
                    isClosable: true,
                    autoHiding: 3000,
                });
            } finally {
                setLoading(false);
            }
        };

        loadFunctions();
    }, [open, instance]);

    useEffect(() => {
        if (open) {
            setName(functionId?.name || '');
        }
    }, [open, functionId]);

    const handleSave = useCallback(() => {
        if (!name.trim()) {
            toaster.add({
                name: 'validation-error',
                title: 'Validation Error',
                content: 'Function name is required',
                theme: 'warning',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }

        onSave({ name: name.trim() });
        onClose();
    }, [name, onClose, onSave]);

    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault();
                handleSave();
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [handleSave, open]);

    const selectOptions = availableFunctions.map(f => ({
        value: f.name || '',
        content: f.name || 'Unknown',
    }));

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Select Function" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', minWidth: '300px' }}>
                    {loading ? (
                        <Box style={{ display: 'flex', justifyContent: 'center', padding: '20px' }}>
                            <Loader size="m" />
                        </Box>
                    ) : availableFunctions.length > 0 ? (
                        <Box>
                            <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                                Select from available functions
                            </Text>
                            <Select
                                value={name ? [name] : []}
                                onUpdate={(values) => setName(values[0] || '')}
                                options={selectOptions}
                                placeholder="Select a function"
                                width="max"
                            />
                        </Box>
                    ) : null}
                    <Box>
                        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                            {availableFunctions.length > 0 ? 'Or enter function name manually' : 'Function Name'}
                        </Text>
                        <TextInput
                            value={name}
                            onUpdate={setName}
                            placeholder="e.g., my-function"
                            autoFocus={availableFunctions.length === 0}
                        />
                    </Box>
                    <Text variant="body-1" color="secondary">
                        Select an existing function or enter a name for a function that will be created later.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleSave}
                textButtonApply="Save"
                textButtonCancel="Cancel"
            />
        </Dialog>
    );
};
