import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { EmptyState } from './EmptyState';

describe('EmptyState', () => {
    afterEach(() => {
        cleanup();
    });

    it('renders the message in default mode', () => {
        render(<EmptyState message="No data" />);
        expect(screen.getByText('No data')).toBeInTheDocument();
    });

    it('renders the message in compact mode', () => {
        render(<EmptyState message="Empty" compact />);
        expect(screen.getByText('Empty')).toBeInTheDocument();
    });

    it('applies the compact modifier class only in compact mode', () => {
        const { container, rerender } = render(<EmptyState message="X" />);
        // Default: the message lives inside a flex wrapper; nothing has the compact modifier.
        expect(container.querySelector('.empty-state--compact')).toBeNull();
        expect(container.querySelector('.empty-state')).not.toBeNull();

        rerender(<EmptyState message="X" compact />);
        expect(container.querySelector('.empty-state--compact')).not.toBeNull();
    });

    it('handles an empty message without throwing', () => {
        expect(() => render(<EmptyState message="" />)).not.toThrow();
    });
});
