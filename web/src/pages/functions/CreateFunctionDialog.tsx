import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Dialog, TextInput } from '@gravity-ui/uikit';
import { API } from '../../api';
import { toaster } from '../../utils';

export interface CreateFunctionDialogProps {
    open: boolean;
    onClose: () => void;
    onCreated: () => void;
    instance: number;
}

export const CreateFunctionDialog: React.FC<CreateFunctionDialogProps> = ({
    open,
    onClose,
    onCreated,
    instance,
}) => {
    const [name, setName] = useState('');
    const [creating, setCreating] = useState(false);

    const handleCreate = useCallback(async () => {
        if (!name.trim()) {
            toaster.warning('validation-error', 'Function name is required', 'Validation Error');
            return;
        }

        setCreating(true);
        try {
            await API.functions.update({
                instance,
                function: {
                    id: { name: name.trim() },
                    chains: [
                        {
                            chain: {
                                name: 'default',
                                modules: [],
                            },
                            weight: '1',
                        },
                    ],
                },
            });

            toaster.success('function-created', `Function "${name}" created`);

            setName('');
            onClose();
            onCreated();
        } catch (err) {
            toaster.error('create-error', 'Failed to create function', err);
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
            <Dialog.Header caption="Create Function" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', minWidth: '300px' }}>
                    <Box>
                        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                            Function Name
                        </Text>
                        <TextInput
                            value={name}
                            onUpdate={setName}
                            placeholder="e.g., my-function"
                            autoFocus
                        />
                    </Box>
                    <Text variant="body-1" color="secondary">
                        A function defines a packet processing chain that can be used in pipelines.
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
