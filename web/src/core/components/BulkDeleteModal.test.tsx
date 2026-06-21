import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import BulkDeleteModal from './BulkDeleteModal';
import { describeDialogKeyboardShortcuts } from './dialogShortcutTests';

describe('BulkDeleteModal', () => {
    afterEach(() => {
        cleanup();
    });

    it('renders nothing when open is false', () => {
        const { container } = render(
            <BulkDeleteModal
                open={false}
                count={3}
                itemNoun="route"
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
            />,
        );
        expect(container.firstChild).toBeNull();
    });

    it('shows "This action cannot be undone" when immediate is true', () => {
        render(
            <BulkDeleteModal
                open
                count={2}
                itemNoun="route"
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
                immediate
            />,
        );
        expect(screen.getByText(/This action cannot be undone/i)).toBeInTheDocument();
    });

    it('shows draft staging text when immediate is omitted', () => {
        render(
            <BulkDeleteModal
                open
                count={2}
                itemNoun="route"
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
            />,
        );
        expect(screen.getByText(/Changes are staged in the draft/i)).toBeInTheDocument();
    });

    it('shows draft staging text when immediate is explicitly false', () => {
        render(
            <BulkDeleteModal
                open
                count={2}
                itemNoun="route"
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
                immediate={false}
            />,
        );
        expect(screen.getByText(/Changes are staged in the draft/i)).toBeInTheDocument();
    });

    describeDialogKeyboardShortcuts(({ onConfirm, onClose }) => (
        <BulkDeleteModal
            open
            count={3}
            itemNoun="route"
            configName="main"
            onClose={onClose}
            onConfirm={onConfirm}
        />
    ));

    it('does not call onConfirm when dialog is closed and Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <BulkDeleteModal
                open={false}
                count={3}
                itemNoun="route"
                configName="main"
                onClose={() => {}}
                onConfirm={onConfirm}
            />,
        );
        fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true });
        expect(onConfirm).not.toHaveBeenCalled();
    });

    it('does not call onClose when dialog is closed and Escape is pressed', () => {
        const onClose = vi.fn();
        render(
            <BulkDeleteModal
                open={false}
                count={3}
                itemNoun="route"
                configName="main"
                onClose={onClose}
                onConfirm={() => {}}
            />,
        );
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).not.toHaveBeenCalled();
    });
});
