import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react';
import RingsPanel, {
    parseCapacityInput,
    isValidPublishBatchInput,
    resolvePublishBatchForSubmit,
} from './RingsPanel';
import { RING_MAX_CAPACITY, RING_MAX_PUBLISH_BATCH } from '@yanet/core/api/ring';
import { PDUMP_MIN_RING_CAPACITY } from './ringSelection';

describe('parseCapacityInput', () => {
    it('accepts a power-of-two byte count', () => {
        expect(parseCapacityInput('1048576')).toBe(1048576n);
    });

    it('rejects a byte count that is not a power of two', () => {
        expect(parseCapacityInput('1000000')).toBeNull();
    });

    it('rejects non-numeric input', () => {
        expect(parseCapacityInput('abc')).toBeNull();
    });

    it('rejects a power of two below the pdump minimum, even one the ring service would accept', () => {
        expect(parseCapacityInput('8')).toBeNull();
        expect(parseCapacityInput(String(PDUMP_MIN_RING_CAPACITY / 2))).toBeNull();
        expect(parseCapacityInput(String(PDUMP_MIN_RING_CAPACITY))).toBe(BigInt(PDUMP_MIN_RING_CAPACITY));
    });

    it('rejects a power of two above the request\'s 32-bit bound', () => {
        expect(parseCapacityInput(String(RING_MAX_CAPACITY + 1))).toBeNull();
        expect(parseCapacityInput('2147483648')).toBe(2147483648n);
    });
});

describe('isValidPublishBatchInput', () => {
    it('accepts an empty value, meaning the service default', () => {
        expect(isValidPublishBatchInput('')).toBe(true);
        expect(isValidPublishBatchInput('  ')).toBe(true);
    });

    it('accepts an integer up to the service maximum', () => {
        expect(isValidPublishBatchInput('0')).toBe(true);
        expect(isValidPublishBatchInput(String(RING_MAX_PUBLISH_BATCH))).toBe(true);
    });

    it('rejects a value above the service maximum', () => {
        expect(isValidPublishBatchInput(String(RING_MAX_PUBLISH_BATCH + 1))).toBe(false);
    });

    it('rejects non-numeric input', () => {
        expect(isValidPublishBatchInput('abc')).toBe(false);
        expect(isValidPublishBatchInput('-1')).toBe(false);
    });
});

describe('resolvePublishBatchForSubmit', () => {
    it('omits the field for an empty value, so the service applies its default', () => {
        expect(resolvePublishBatchForSubmit('')).toBeUndefined();
    });

    it('sends the typed value otherwise', () => {
        expect(resolvePublishBatchForSubmit('16')).toBe(16);
    });
});

describe('RingsPanel create form', () => {
    afterEach(() => {
        cleanup();
    });

    const fillDraft = (container: HTMLElement): void => {
        const [nameInput, capacityInput] = container.querySelectorAll('input');
        fireEvent.change(nameInput!, { target: { value: 'ring0' } });
        fireEvent.change(capacityInput!, { target: { value: '1048576' } });
    };

    it('keeps the typed name and capacity when the service rejects the create', async () => {
        const onCreate = vi.fn().mockResolvedValue(false);
        const { container } = render(
            <RingsPanel rings={[]} loading={false} busy={false} onCreate={onCreate} onDeleteRequest={() => {}} />,
        );
        fillDraft(container);

        const createButton = container.querySelector('.yn-table-action-btn:not(.yn-table-action-btn--delete)');
        fireEvent.click(createButton!);

        await waitFor(() => expect(onCreate).toHaveBeenCalledWith('ring0', 1048576, undefined));
        const [nameInput, capacityInput] = container.querySelectorAll('input');
        expect((nameInput as HTMLInputElement).value).toBe('ring0');
        expect((capacityInput as HTMLInputElement).value).toBe('1048576');
    });

    it('refuses a create while the ring list is still loading', () => {
        const onCreate = vi.fn().mockResolvedValue(true);
        const { container } = render(
            <RingsPanel rings={[]} loading busy={false} onCreate={onCreate} onDeleteRequest={() => {}} />,
        );
        fillDraft(container);

        const createButton = container.querySelector('.yn-table-action-btn:not(.yn-table-action-btn--delete)');
        expect((createButton as HTMLButtonElement).disabled).toBe(true);
        fireEvent.click(createButton!);
        expect(onCreate).not.toHaveBeenCalled();
    });

    it('clears the draft once the service confirms the ring was created', async () => {
        const onCreate = vi.fn().mockResolvedValue(true);
        const { container } = render(
            <RingsPanel rings={[]} loading={false} busy={false} onCreate={onCreate} onDeleteRequest={() => {}} />,
        );
        fillDraft(container);

        const createButton = container.querySelector('.yn-table-action-btn:not(.yn-table-action-btn--delete)');
        fireEvent.click(createButton!);

        const [nameInput, capacityInput] = container.querySelectorAll('input');
        await waitFor(() => expect((nameInput as HTMLInputElement).value).toBe(''));
        expect((capacityInput as HTMLInputElement).value).toBe('');
    });
});

describe('RingsPanel ring list', () => {
    afterEach(() => {
        cleanup();
    });

    it('marks only a ring below the pdump minimum as too small for pdump', () => {
        const { container } = render(
            <RingsPanel
                rings={[
                    { name: 'small', capacity: String(PDUMP_MIN_RING_CAPACITY / 2) },
                    { name: 'fits', capacity: PDUMP_MIN_RING_CAPACITY },
                ]}
                loading={false}
                busy={false}
                onCreate={vi.fn()}
                onDeleteRequest={() => {}}
            />,
        );

        const rows = Array.from(container.querySelectorAll('.pdump-rings-panel__row'));
        const warned = rows.filter((row) => row.querySelector('.pdump-rings-panel__meta--warn') !== null);
        expect(warned.map((row) => row.querySelector('.pdump-rings-panel__name')?.textContent)).toEqual(['small']);
    });
});
