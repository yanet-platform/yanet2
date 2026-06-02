import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { ConfirmModal } from './ConfirmModal';

describe('ConfirmModal', () => {
    afterEach(() => {
        cleanup();
    });

    it('renders nothing when open is false', () => {
        const { container } = render(
            <ConfirmModal
                open={false}
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>Are you sure?</p>
            </ConfirmModal>,
        );
        expect(container.firstChild).toBeNull();
    });

    it('renders title in .yn-modal__title', () => {
        const { container } = render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>Are you sure?</p>
            </ConfirmModal>,
        );
        const title = container.querySelector('.yn-modal__title');
        expect(title?.textContent).toBe('Delete item');
    });

    it('renders children in .yn-modal__body', () => {
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>Are you sure?</p>
            </ConfirmModal>,
        );
        expect(screen.getByText('Are you sure?')).toBeInTheDocument();
    });

    it('renders confirm and cancel buttons', () => {
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        expect(screen.getByText('Delete')).toBeInTheDocument();
        expect(screen.getByText('Cancel')).toBeInTheDocument();
    });

    it('uses custom cancelText when provided', () => {
        render(
            <ConfirmModal
                open
                title="Confirm"
                confirmText="Yes"
                cancelText="No"
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        expect(screen.getByText('No')).toBeInTheDocument();
    });

    it('shows busyText when busy is true', () => {
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                busyText="Deleting…"
                busy
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        expect(screen.getByText('Deleting…')).toBeInTheDocument();
        expect(screen.queryByText('Delete')).toBeNull();
    });

    it('disables both buttons when busy is true', () => {
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                busyText="Deleting…"
                busy
                onClose={() => {}}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        const buttons = screen.getAllByRole('button');
        const cancelBtn = buttons.find((b) => b.textContent === 'Cancel');
        const confirmBtn = buttons.find((b) => b.textContent === 'Deleting…');
        expect(cancelBtn).toBeDisabled();
        expect(confirmBtn).toBeDisabled();
    });

    it('calls onConfirm when Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={onConfirm}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onConfirm when Cmd+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={() => {}}
                onConfirm={onConfirm}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        fireEvent.keyDown(document, { key: 'Enter', metaKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onClose when Escape is pressed', () => {
        const onClose = vi.fn();
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                onClose={onClose}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does not call onConfirm when busy and Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                busy
                onClose={() => {}}
                onConfirm={onConfirm}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true });
        expect(onConfirm).not.toHaveBeenCalled();
    });

    it('does not call onClose when busy and Escape is pressed', () => {
        const onClose = vi.fn();
        render(
            <ConfirmModal
                open
                title="Delete item"
                confirmText="Delete"
                busy
                onClose={onClose}
                onConfirm={() => {}}
            >
                <p>body</p>
            </ConfirmModal>,
        );
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).not.toHaveBeenCalled();
    });

    it('stops propagation to window on Ctrl+Enter so page shortcuts are not triggered', () => {
        const onConfirm = vi.fn();
        const onClose = vi.fn();
        const windowSpy = vi.fn();
        window.addEventListener('keydown', windowSpy);
        try {
            render(
                <ConfirmModal
                    open
                    title="Delete item"
                    confirmText="Delete"
                    onClose={onClose}
                    onConfirm={onConfirm}
                >
                    <p>body</p>
                </ConfirmModal>,
            );
            fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true, bubbles: true });
            expect(onConfirm).toHaveBeenCalledTimes(1);
            expect(windowSpy).not.toHaveBeenCalled();
        } finally {
            window.removeEventListener('keydown', windowSpy);
        }
    });

    it('stops propagation to window on Escape so page shortcuts are not triggered', () => {
        const onClose = vi.fn();
        const windowSpy = vi.fn();
        window.addEventListener('keydown', windowSpy);
        try {
            render(
                <ConfirmModal
                    open
                    title="Delete item"
                    confirmText="Delete"
                    onClose={onClose}
                    onConfirm={() => {}}
                >
                    <p>body</p>
                </ConfirmModal>,
            );
            fireEvent.keyDown(document, { key: 'Escape', bubbles: true });
            expect(onClose).toHaveBeenCalledTimes(1);
            expect(windowSpy).not.toHaveBeenCalled();
        } finally {
            window.removeEventListener('keydown', windowSpy);
        }
    });
});
