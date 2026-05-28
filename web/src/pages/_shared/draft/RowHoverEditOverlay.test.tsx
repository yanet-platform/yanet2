import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import RowHoverEditOverlay from './RowHoverEditOverlay';

describe('RowHoverEditOverlay', () => {
    afterEach(() => {
        cleanup();
    });

    it('renders a single edit button when onDelete is not provided', () => {
        render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        const buttons = screen.getAllByRole('button');
        expect(buttons).toHaveLength(1);
        expect(buttons[0].getAttribute('aria-label')).toBe('Edit row 1');
    });

    it('does not apply the wide class when onDelete is absent', () => {
        const { container } = render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        expect(container.querySelector('.fw-row-action-slot--wide')).toBeNull();
        expect(container.querySelector('.fw-row-action-slot')).not.toBeNull();
    });

    it('renders two buttons when onDelete is provided', () => {
        render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onDelete={() => {}}
                deleteAriaLabel="Delete row 1"
                deleteTitle="Delete"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        const buttons = screen.getAllByRole('button');
        expect(buttons).toHaveLength(2);
    });

    it('delete button has danger class and correct aria-label', () => {
        render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onDelete={() => {}}
                deleteAriaLabel="Delete row 1"
                deleteTitle="Delete"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        const dangerBtn = document.querySelector('.fw-row-edit-btn--danger') as HTMLElement;
        expect(dangerBtn).not.toBeNull();
        expect(dangerBtn.getAttribute('aria-label')).toBe('Delete row 1');
    });

    it('applies the wide class to the slot when onDelete is provided', () => {
        const { container } = render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onDelete={() => {}}
                deleteAriaLabel="Delete row 1"
                deleteTitle="Delete"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        expect(container.querySelector('.fw-row-action-slot--wide')).not.toBeNull();
    });

    it('fires onEdit when the edit button is clicked', () => {
        const onEdit = vi.fn();
        render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={onEdit}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        fireEvent.click(screen.getByRole('button', { name: 'Edit row 1' }));
        expect(onEdit).toHaveBeenCalledTimes(1);
    });

    it('fires onDelete when the delete button is clicked', () => {
        const onDelete = vi.fn();
        render(
            <RowHoverEditOverlay
                top={40}
                rowHeight={32}
                onEdit={() => {}}
                editAriaLabel="Edit row 1"
                editTitle="Edit"
                onDelete={onDelete}
                deleteAriaLabel="Delete row 1"
                deleteTitle="Delete"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        fireEvent.click(screen.getByRole('button', { name: 'Delete row 1' }));
        expect(onDelete).toHaveBeenCalledTimes(1);
    });

    it('passes top and rowHeight into inline styles', () => {
        const { container } = render(
            <RowHoverEditOverlay
                top={128}
                rowHeight={48}
                onEdit={() => {}}
                editAriaLabel="Edit"
                editTitle="Edit"
                onMouseEnter={() => {}}
                onMouseLeave={() => {}}
            />,
        );
        const slot = container.querySelector('.fw-row-action-slot') as HTMLElement;
        expect(slot.style.top).toBe('128px');
        expect(slot.style.height).toBe('48px');
    });
});
