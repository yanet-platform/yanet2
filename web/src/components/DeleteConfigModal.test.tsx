import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import DeleteConfigModal from './DeleteConfigModal';

describe('DeleteConfigModal', () => {
    afterEach(() => {
        cleanup();
    });

    it('renders nothing when open is false', () => {
        const { container } = render(
            <DeleteConfigModal
                open={false}
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
            />,
        );
        expect(container.firstChild).toBeNull();
    });

    it('shows "Delete config" title by default', () => {
        const { container } = render(
            <DeleteConfigModal
                open
                configName="main"
                onClose={() => {}}
                onConfirm={() => {}}
            />,
        );
        const title = container.querySelector('.yn-modal__title');
        expect(title?.textContent).toBe('Delete config');
    });

    it('shows "Delete table" title when noun="table"', () => {
        const { container } = render(
            <DeleteConfigModal
                open
                configName="my-table"
                onClose={() => {}}
                onConfirm={() => {}}
                noun="table"
            />,
        );
        const title = container.querySelector('.yn-modal__title');
        expect(title?.textContent).toBe('Delete table');
    });

    it('uses the noun in the body text', () => {
        const { container } = render(
            <DeleteConfigModal
                open
                configName="my-table"
                onClose={() => {}}
                onConfirm={() => {}}
                noun="table"
            />,
        );
        const body = container.querySelector('.yn-modal__body p');
        expect(body?.textContent).toMatch(/Delete table/);
        expect(screen.getByText('my-table')).toBeInTheDocument();
    });

    it('calls onConfirm when Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <DeleteConfigModal
                open
                configName="main"
                onClose={() => {}}
                onConfirm={onConfirm}
            />,
        );
        fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onConfirm when Cmd+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <DeleteConfigModal
                open
                configName="main"
                onClose={() => {}}
                onConfirm={onConfirm}
            />,
        );
        fireEvent.keyDown(document, { key: 'Enter', metaKey: true });
        expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('calls onClose when Escape is pressed', () => {
        const onClose = vi.fn();
        render(
            <DeleteConfigModal
                open
                configName="main"
                onClose={onClose}
                onConfirm={() => {}}
            />,
        );
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does not call onConfirm when dialog is closed and Ctrl+Enter is pressed', () => {
        const onConfirm = vi.fn();
        render(
            <DeleteConfigModal
                open={false}
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
            <DeleteConfigModal
                open={false}
                configName="main"
                onClose={onClose}
                onConfirm={() => {}}
            />,
        );
        fireEvent.keyDown(document, { key: 'Escape' });
        expect(onClose).not.toHaveBeenCalled();
    });

    it('stops propagation to window on Ctrl+Enter so draft page shortcuts are not triggered', () => {
        const onConfirm = vi.fn();
        const onClose = vi.fn();
        const windowSpy = vi.fn();
        window.addEventListener('keydown', windowSpy);
        try {
            render(
                <DeleteConfigModal
                    open
                    configName="main"
                    onClose={onClose}
                    onConfirm={onConfirm}
                />,
            );
            fireEvent.keyDown(document, { key: 'Enter', ctrlKey: true, bubbles: true });
            expect(onConfirm).toHaveBeenCalledTimes(1);
            expect(windowSpy).not.toHaveBeenCalled();
        } finally {
            window.removeEventListener('keydown', windowSpy);
        }
    });

    it('stops propagation to window on Escape so draft page shortcuts are not triggered', () => {
        const onConfirm = vi.fn();
        const onClose = vi.fn();
        const windowSpy = vi.fn();
        window.addEventListener('keydown', windowSpy);
        try {
            render(
                <DeleteConfigModal
                    open
                    configName="main"
                    onClose={onClose}
                    onConfirm={onConfirm}
                />,
            );
            fireEvent.keyDown(document, { key: 'Escape', bubbles: true });
            expect(onClose).toHaveBeenCalledTimes(1);
            expect(windowSpy).not.toHaveBeenCalled();
        } finally {
            window.removeEventListener('keydown', windowSpy);
        }
    });
});
