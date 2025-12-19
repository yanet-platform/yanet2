import React from 'react';
import { ConfirmDialog } from '../../../components';
import type { DeleteConfigDialogProps } from '../types';

export const DeleteConfigDialog: React.FC<DeleteConfigDialogProps> = ({
    open,
    onClose,
    onConfirm,
    configName,
}) => {
    return (
        <ConfirmDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            title="Delete ACL Config"
            message={`Are you sure you want to delete the ACL config "${configName}"?`}
            secondaryMessage="This action cannot be undone. All rules in this config will be permanently deleted."
            confirmText="Delete"
            cancelText="Cancel"
            danger
        />
    );
};
