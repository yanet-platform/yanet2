import { it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import React from 'react';

interface Handlers {
    onConfirm: () => void;
    onClose: () => void;
}

/**
 * Registers the three standard keyboard-shortcut specs for a dialog modal.
 *
 * Call inside a `describe` block and pass a factory that renders the modal
 * with the provided handlers. Test names are intentionally stable so that
 * vitest output stays consistent across suites.
 */
export const describeDialogKeyboardShortcuts = (
    renderDialog: (handlers: Handlers) => React.ReactElement,
): void => {
    it('calls onConfirm when Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(renderDialog({ onConfirm, onClose: () => {} }));
        fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onConfirm when Cmd+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(renderDialog({ onConfirm, onClose: () => {} }));
        fireEvent.keyDown(document, { key: 'Enter', metaKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onClose when Escape is pressed', () => {
        const onClose = vi.fn();
        render(renderDialog({ onConfirm: () => {}, onClose }));
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).toHaveBeenCalledTimes(1);
    });
};
