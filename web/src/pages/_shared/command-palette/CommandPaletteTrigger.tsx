import React from 'react';
import { Icon } from '@gravity-ui/uikit';
import { Magnifier } from '@gravity-ui/icons';

/** Platform-correct palette shortcut label: ⌘K on macOS, Ctrl+K elsewhere. */
export const PALETTE_SHORTCUT_LABEL = /mac/i.test(navigator.platform || navigator.userAgent) ? '⌘K' : 'Ctrl+K';

interface CommandPaletteTriggerProps {
    placeholder: string;
    onOpen: () => void;
}

/** Header pill that opens the command palette; shows the platform shortcut. */
const CommandPaletteTrigger: React.FC<CommandPaletteTriggerProps> = ({ placeholder, onOpen }) => (
    <button
        type="button"
        className="cp-trigger"
        onClick={onOpen}
        title={`Open command palette (${PALETTE_SHORTCUT_LABEL})`}
    >
        <Icon data={Magnifier} size={16} />
        <span className="cp-trigger__placeholder">{placeholder}</span>
        <kbd className="cp-kbd">{PALETTE_SHORTCUT_LABEL}</kbd>
    </button>
);

export default CommandPaletteTrigger;
