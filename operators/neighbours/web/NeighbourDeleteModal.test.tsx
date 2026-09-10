import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NeighbourDeleteModal } from './NeighbourDeleteModal';

afterEach(cleanup);

describe('neighbour removal preview', () => {
    it('bounds rendered rows while reporting and confirming the full affected scope', () => {
        const affected = Array.from({ length: 1000 }, (_, idx) => ({
            next_hop: 'fe80::1', device: `logical${idx}`,
        }));
        const confirm = vi.fn();
        render(<NeighbourDeleteModal table="static" affected={affected} onClose={vi.fn()} onConfirm={confirm} />);

        expect(screen.getAllByRole('listitem')).toHaveLength(50);
        expect(screen.getByText(/This affects 1000 currently listed entries/)).toBeInTheDocument();
        expect(screen.getByText(/950 more are also affected/)).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Delete all device variants' }));
        expect(confirm).toHaveBeenCalledOnce();
    });
});
