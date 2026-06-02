import React from 'react';
import { ConfirmModal } from './ConfirmModal';

interface BulkDeleteModalProps {
    open: boolean;
    count: number;
    itemNoun: string;
    configName: string;
    onClose: () => void;
    onConfirm: () => void;
    /** Live-write pages (operators/*) set this to switch the hint sentence to a non-draft wording. */
    immediate?: boolean;
}

/** Modal confirming bulk deletion of selected rows. */
const BulkDeleteModal: React.FC<BulkDeleteModalProps> = ({
    open,
    count,
    itemNoun,
    configName,
    onClose,
    onConfirm,
    immediate = false,
}) => (
    <ConfirmModal
        open={open}
        title={`Delete ${itemNoun}s`}
        confirmText={`Delete ${count} ${itemNoun}(s)`}
        onClose={onClose}
        onConfirm={onConfirm}
    >
        <p>
            Delete <strong>{count}</strong> selected {itemNoun}(s) from <code>{configName}</code>?
            {' '}
            {immediate
                ? 'This action cannot be undone.'
                : 'Changes are staged in the draft; discard the draft to revert.'}
        </p>
    </ConfirmModal>
);

export default BulkDeleteModal;
