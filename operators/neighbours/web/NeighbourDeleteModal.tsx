import React from 'react';
import { ConfirmModal } from '@yanet/core/components/ConfirmModal';
import type { Neighbour } from '@yanet/core/api/neighbours';
import { getNeighbourId, getNeighbourNextHop } from './utils';

interface NeighbourDeleteModalProps {
    open: boolean;
    table: string;
    affected: Neighbour[];
    onClose: () => void;
    onConfirm: () => void;
}

/** Shows every currently known device variant covered by IP-wide removal. */
export const NeighbourDeleteModal: React.FC<NeighbourDeleteModalProps> = ({
    open, table, affected, onClose, onConfirm,
}) => (
    <ConfirmModal
        open={open}
        title="Delete IPs across all devices"
        confirmText="Delete all device variants"
        onClose={onClose}
        onConfirm={onConfirm}
    >
        <p>
            Delete every device variant of these IPs from <code>{table}</code>?
            {' '}This affects {affected.length} currently listed entries, including variants
            you did not select. Any new variants of the same IPs are also removed.
        </p>
        <ul style={{ maxHeight: 240, overflowY: 'auto' }}>
            {affected.map((entry) => (
                <li key={getNeighbourId(entry)}>
                    <code>{getNeighbourNextHop(entry)}</code> on <code>{entry.device || '(unscoped)'}</code>
                </li>
            ))}
        </ul>
        <p>This action cannot be undone.</p>
    </ConfirmModal>
);
