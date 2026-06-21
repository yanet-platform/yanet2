import React from 'react';
import type { PrefixRowItem, PrefixRowErrors } from './types';
import { validateRow } from './validation';
import { DraftItemDrawer } from '@yanet/core/components/draft';
import { CidrPrefixField } from '@yanet/core/components';
import { useRowDraft } from '@yanet/core/hooks';

interface PrefixDrawerProps {
    open: boolean;
    row: PrefixRowItem | null;
    index: number;
    total: number;
    onClose: () => void;
    /** Called when the user confirms the form. Updates local draft only — no API call. */
    onChange: (updated: PrefixRowItem) => void;
    onDelete: (row: PrefixRowItem) => void;
    onJump: (delta: number) => void;
}

export interface PrefixDrawerHandle {
    /** Flush any pending state and apply. Returns false if closed or invalid. */
    flushAndApply(): boolean;
}

/** Side drawer for adding/editing a single decap prefix row. */
const PrefixDrawer = React.forwardRef<PrefixDrawerHandle, PrefixDrawerProps>(({
    open,
    row,
    index,
    total,
    onClose,
    onChange,
    onDelete,
    onJump,
}, ref) => {
    const { draft, errors, updateField, handleApply } = useRowDraft<PrefixRowItem, PrefixRowErrors>({
        open, row, emptyErrors: { prefix: null }, validateRow, onChange, onClose, handleRef: ref,
    });

    return (
        <DraftItemDrawer
            open={open}
            index={index}
            total={total}
            titleSingular="prefix"
            onClose={onClose}
            onApply={handleApply}
            onDelete={draft ? () => onDelete(draft) : undefined}
            onJump={onJump}
            ariaLabel="Edit prefix"
        >
            <section className="yn-section">
                <div className="yn-section-h">Prefix</div>
                <div className="yn-section__body">
                    <CidrPrefixField
                        label="CIDR"
                        placeholder="10.0.0.0/8 or 2a02:6b8::/32"
                        value={draft?.prefix ?? ''}
                        error={errors.prefix}
                        onChange={(v) => updateField('prefix', v.trim())}
                    />
                </div>
            </section>
        </DraftItemDrawer>
    );
});

PrefixDrawer.displayName = 'PrefixDrawer';

export default PrefixDrawer;
