import React from 'react';
import DraftActionButtons from './DraftActionButtons';

/** Minimum shape required of a rule item to build common table props. */
interface RuleItemShape {
    id: string;
    index: number;
}

interface RuleTableCommonOptions<T extends RuleItemShape> {
    items: T[];
    onEditRule: (item: T) => void;
    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
    activeRowId: string | null;
    flashRowId?: string | null;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
}

interface RuleTableCommonResult<T extends RuleItemShape> {
    emptyMessage: string;
    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
    sortState: { column: null; direction: 'asc' };
    onSort: () => void;
    onEditRow: (id: string) => void;
    editAriaLabel: (item: T) => string;
    editTitle: string;
    activeRowId: string | null;
    flashRowId?: string | null;
    headerActions: React.ReactNode;
    footerExtra: React.ReactNode;
}

/** Returns the VirtualTable prop subset that is identical across ACL and Forward rule tables. */
const ruleTableCommonProps = <T extends RuleItemShape>(
    options: RuleTableCommonOptions<T>,
): RuleTableCommonResult<T> => {
    const {
        items,
        onEditRule,
        selectedIds,
        onSelectionChange,
        activeRowId,
        flashRowId,
        currentIsDirty,
        onSave,
        onDiscard,
        onDeleteConfig,
    } = options;

    return {
        emptyMessage: 'No rules match your search.',
        selectedIds,
        onSelectionChange,
        sortState: { column: null, direction: 'asc' },
        onSort: () => {},
        onEditRow: (id: string) => {
            const it = items.find((item) => item.id === id);
            if (it) onEditRule(it);
        },
        editAriaLabel: (item: T) => `Edit rule ${item.index + 1}`,
        editTitle: 'Edit rule',
        activeRowId,
        flashRowId,
        headerActions: (
            <DraftActionButtons
                currentIsDirty={currentIsDirty}
                onSave={onSave}
                onDiscard={onDiscard}
                onDeleteConfig={onDeleteConfig}
            />
        ),
        footerExtra: selectedIds.size > 0 ? (
            <span className="yn-toolbar__count" style={{ color: 'var(--yn-accent)' }}>
                {selectedIds.size.toLocaleString()} selected
            </span>
        ) : undefined,
    };
};

export { ruleTableCommonProps };
export type { RuleTableCommonOptions, RuleTableCommonResult };
