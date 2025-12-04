import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Dialog, TextInput } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { API } from '../../api';

export interface CreatePipelineDialogProps {
    open: boolean;
    onClose: () => void;
    onCreated: () => void;
    instance: number;
}

export const CreatePipelineDialog: React.FC<CreatePipelineDialogProps> = ({
    open,
    onClose,
    onCreated,
    instance,
}) => {
    const [name, setName] = useState('');
    const [creating, setCreating] = useState(false);

    const handleCreate = useCallback(async () => {
        if (!name.trim()) {
            toaster.add({
                name: 'validation-error',
                title: 'Validation Error',
                content: 'Pipeline name is required',
                theme: 'warning',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }

        setCreating(true);
        try {
            await API.pipelines.update({
                instance,
                pipeline: {
                    id: { name: name.trim() },
                    functions: [],
                },
            });

            toaster.add({
                name: 'pipeline-created',
                title: 'Success',
                content: `Pipeline "${name}" created`,
                theme: 'success',
                isClosable: true,
                autoHiding: 3000,
            });

            setName('');
            onClose();
            onCreated();
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'create-error',
                title: 'Error',
                content: `Failed to create pipeline: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
        } finally {
            setCreating(false);
        }
    }, [instance, name, onClose, onCreated]);

    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault();
                handleCreate();
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [handleCreate, open]);

    const handleClose = () => {
        setName('');
        onClose();
    };

    return (
        <Dialog open={open} onClose={handleClose}>
            <Dialog.Header caption="Create Pipeline" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', minWidth: '300px' }}>
                    <Box>
                        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                            Pipeline Name
                        </Text>
                        <TextInput
                            value={name}
                            onUpdate={setName}
                            placeholder="e.g., my-pipeline"
                            autoFocus
                        />
                    </Box>
                    <Text variant="body-1" color="secondary">
                        A pipeline defines a sequence of network functions that process packets.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={handleClose}
                onClickButtonApply={handleCreate}
                textButtonApply="Create"
                textButtonCancel="Cancel"
                loading={creating}
            />
        </Dialog>
    );
};

