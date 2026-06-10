import React from 'react';

/** Returns the footer meta text for a rule drawer based on mode and rule index. */
const ruleDrawerFootMeta = (mode: 'add' | 'edit', ruleItem: { index: number } | null): string =>
    mode === 'add' ? 'Will be appended to config.' : `Rule #${(ruleItem?.index ?? -1) + 1}`;

interface RuleDrawerFootActionsProps {
    onCancel: () => void;
    onApply: () => void;
    /** When true, the Apply button is disabled. Omit or pass undefined to leave it enabled. */
    applyDisabled?: boolean;
}

/** Foot-action row shared by ACL and Forward rule drawers. */
const RuleDrawerFootActions: React.FC<RuleDrawerFootActionsProps> = ({
    onCancel,
    onApply,
    applyDisabled,
}) => (
    <>
        <button type="button" className="yn-btn yn-btn--ghost" onClick={onCancel}>
            Cancel
        </button>
        <button
            type="button"
            className="yn-btn yn-btn--primary"
            disabled={applyDisabled}
            onClick={onApply}
        >
            Apply
        </button>
    </>
);

export { ruleDrawerFootMeta };
export default RuleDrawerFootActions;
