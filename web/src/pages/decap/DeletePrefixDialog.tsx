import React, { useCallback } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { ConfirmDialog } from '../../components';
import type { DeletePrefixDialogProps } from './types';
import './decap.css';

export const DeletePrefixDialog: React.FC<DeletePrefixDialogProps> = ({
    open,
    onClose,
    onConfirm,
    selectedPrefixes,
}) => {
    const handleConfirm = useCallback(async () => {
        await onConfirm();
        onClose();
    }, [onConfirm, onClose]);

    const prefixCount = selectedPrefixes.length;
    const maxDisplayed = 10;
    const displayedPrefixes = selectedPrefixes.slice(0, maxDisplayed);
    const remainingCount = prefixCount - maxDisplayed;

    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Delete Prefixes"
            message={`Are you sure you want to delete ${prefixCount} prefix(es)? Press Ctrl+Enter to confirm.`}
            confirmText="Delete"
            danger
            disabled={prefixCount === 0}
        >
            <Box className="decap-delete-dialog__list">
                <Box component="ul" className="decap-delete-dialog__ul">
                    {displayedPrefixes.map((prefix, idx) => (
                        <li key={idx}>
                            <Text variant="code-1">{prefix}</Text>
                        </li>
                    ))}
                    {remainingCount > 0 && (
                        <li>
                            <Text variant="body-1" color="secondary">
                                ... and {remainingCount} more
                            </Text>
                        </li>
                    )}
                </Box>
            </Box>
        </ConfirmDialog>
    );
};
