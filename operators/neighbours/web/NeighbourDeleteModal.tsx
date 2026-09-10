import React from 'react';
import { ConfirmModal } from '@yanet/core/components/ConfirmModal';
import type { Neighbour } from '@yanet/core/api/neighbours';
import { getNeighbourId, getNeighbourNextHop } from './utils';

interface NeighbourDeleteModalProps {
    table: string;
    affected: Neighbour[];
    onClose: () => void;
    onConfirm: () => void;
}

const PREVIEW_LIMIT = 50;

/** Shows a bounded preview and the full scope of IP-wide removal. */
export const NeighbourDeleteModal: React.FC<NeighbourDeleteModalProps> = ({
    table, affected, onClose, onConfirm,
}) => (
    <ConfirmModal
        open
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
            {affected.slice(0, PREVIEW_LIMIT).map((entry) => (
                <li key={getNeighbourId(entry)}>
                    <code>{getNeighbourNextHop(entry)}</code> on <code>{entry.device || '(unscoped)'}</code>
                </li>
            ))}
        </ul>
        {affected.length > PREVIEW_LIMIT && (
            <p>Showing the first {PREVIEW_LIMIT} entries; {affected.length - PREVIEW_LIMIT} more are also affected.</p>
        )}
        <p>This action cannot be undone.</p>
    </ConfirmModal>
);
