import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import BulkDeleteModal from './BulkDeleteModal';

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
});
