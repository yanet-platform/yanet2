import React from 'react';
import { Text } from '@gravity-ui/uikit';
import { CommandPaletteTrigger, usePalette } from '../pages/_shared/command-palette';
import './CommandPaletteHeader.scss';

interface CommandPaletteHeaderProps {
    /** Page title shown in the left column. */
    title: React.ReactNode;
    /** Placeholder text for the command-palette trigger pill. */
    placeholder: string;
    /** Optional right-aligned action controls. */
    actions?: React.ReactNode;
}

/** Standard page header: title, centered command-palette trigger, and actions.
 *
 * Wraps the shared `page-header-bar` grid and wires the trigger to the global
 * palette via usePalette, so every page renders the trigger in the identical
 * centered position without duplicating the markup or the open handler.
 */
const CommandPaletteHeader: React.FC<CommandPaletteHeaderProps> = ({ title, placeholder, actions }) => {
    const { openPalette } = usePalette();
    return (
        <div className="page-header-bar">
            <Text variant="header-1">{title}</Text>
            <CommandPaletteTrigger placeholder={placeholder} onOpen={openPalette} />
            <div className="page-header-bar__actions">{actions}</div>
        </div>
    );
};

export default CommandPaletteHeader;
