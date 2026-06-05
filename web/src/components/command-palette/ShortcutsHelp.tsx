import React from 'react';
import type { ShortcutSection } from './types';
import './shortcuts-help.scss';

const GLOBAL_SECTION: ShortcutSection = {
    title: 'Global',
    items: [
        { keys: '⌘K / Ctrl+K', desc: 'Open command palette' },
        { keys: '?', desc: 'Show this help' },
        { keys: '[ ]', desc: 'Cycle config tabs' },
        { keys: '↑ ↓', desc: 'Move selection in a list' },
        { keys: 'Enter', desc: 'Open the selected item' },
        { keys: 'Esc', desc: 'Close overlay / clear selection' },
        { keys: '⌘↵ / Ctrl+Enter', desc: 'Submit a dialog' },
    ],
};

interface ShortcutsHelpProps {
    open: boolean;
    onClose: () => void;
    pageSections: ShortcutSection[] | null;
}

/** Keyboard-shortcuts help overlay. Renders null when closed. */
const ShortcutsHelp: React.FC<ShortcutsHelpProps> = ({ open, onClose, pageSections }) => {
    if (!open) return null;

    const sections = pageSections ? [GLOBAL_SECTION, ...pageSections] : [GLOBAL_SECTION];

    return (
        <div
            className="cp-backdrop sh-backdrop"
            onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className="sh-card">
                <div className="sh-header">
                    <span className="sh-title">Keyboard shortcuts</span>
                    <kbd className="cp-esc-hint sh-esc">esc</kbd>
                </div>
                <div className="sh-body">
                    {sections.map((section) => (
                        <div key={section.title} className="sh-section">
                            <div className="sh-section-title">{section.title}</div>
                            {section.items.map((item) => (
                                <div key={item.keys} className="sh-row">
                                    <span className="sh-keys">
                                        {item.keys.split('/').map((k, idx) => (
                                            <React.Fragment key={idx}>
                                                {idx > 0 && <span className="sh-sep">/</span>}
                                                <kbd className="cp-kbd">{k.trim()}</kbd>
                                            </React.Fragment>
                                        ))}
                                    </span>
                                    <span className="sh-desc">{item.desc}</span>
                                </div>
                            ))}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
};

export default ShortcutsHelp;
