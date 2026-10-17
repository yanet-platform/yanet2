import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { pdumpApi, PDUMP_RATE_PPS_MAX } from '@yanet/core/api/pdump';
import { toaster } from '@yanet/core/utils';
import { ConfigDialog } from './ConfigDialog';

describe('ConfigDialog ring refresh', () => {
    afterEach(() => {
        cleanup();
    });

    const baseProps = {
        onClose: () => {},
        onSaved: () => {},
        rings: [],
        ringsLoading: false,
    };

    it('refetches the shared ring list each time the dialog opens', () => {
        const onRefreshRings = vi.fn();
        const { rerender } = render(
            <ConfigDialog open={false} isCreate {...baseProps} onRefreshRings={onRefreshRings} />,
        );
        expect(onRefreshRings).not.toHaveBeenCalled();

        rerender(<ConfigDialog open isCreate {...baseProps} onRefreshRings={onRefreshRings} />);
        expect(onRefreshRings).toHaveBeenCalledTimes(1);

        // Re-rendering while still open (e.g. the shared ring list updates)
        // must not loop the refetch.
        rerender(<ConfigDialog open isCreate {...baseProps} rings={[{ name: 'ring0' }]} onRefreshRings={onRefreshRings} />);
        expect(onRefreshRings).toHaveBeenCalledTimes(1);

        rerender(<ConfigDialog open={false} isCreate {...baseProps} onRefreshRings={onRefreshRings} />);
        rerender(<ConfigDialog open isCreate {...baseProps} onRefreshRings={onRefreshRings} />);
        expect(onRefreshRings).toHaveBeenCalledTimes(2);
    });
});

vi.mock('@yanet/core/utils', () => ({
    toaster: { error: vi.fn(), success: vi.fn() },
}));

describe('capture limit editing', () => {
    const ringProps = {
        rings: [{ name: 'A' }],
        ringsLoading: false,
        onRefreshRings: vi.fn(),
    };
    afterEach(cleanup);
    beforeEach(() => {
        vi.restoreAllMocks();
        vi.clearAllMocks();
        vi.spyOn(pdumpApi, 'setConfig').mockResolvedValue(undefined);
    });

    it.each([0, 1000, 1_000_000])('saves the exact numeric limit %s', async rate => {
        render(<ConfigDialog {...ringProps} open configName="capture" initialConfig={{ filter: 'udp', mode: 1, ring_name: 'A', rate_pps: rate }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ rate_pps: rate })));
    });

    it('omits a blank rate while saving another field', async () => {
        render(<ConfigDialog {...ringProps} open configName="capture" initialConfig={{ filter: 'udp', mode: 1, ring_name: 'A', rate_pps: 1000 }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(screen.getByRole('textbox', { name: 'Capture limit (pps)' }), { target: { value: '' } });
        fireEvent.change(screen.getByPlaceholderText('tcp port 80'), { target: { value: 'tcp' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ filter: 'tcp', rate_pps: undefined })));
    });

    it('allows explicitly disabling an existing limit', async () => {
        render(<ConfigDialog {...ringProps} open configName="capture" initialConfig={{ filter: 'udp', mode: 1, ring_name: 'A', rate_pps: 1000 }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(screen.getByRole('textbox', { name: 'Capture limit (pps)' }), { target: { value: '0' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ rate_pps: 0 })));
    });

    it.each(['1000001', '9007199254740992', '-1', '1.5', '1e3'])('rejects out-of-range or invalid input %s before sending', value => {
        render(<ConfigDialog {...ringProps} open configName="capture" initialConfig={{ filter: 'udp', mode: 1, ring_name: 'A' }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(screen.getByRole('textbox', { name: 'Capture limit (pps)' }), { target: { value } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(pdumpApi.setConfig).not.toHaveBeenCalled();
        expect(toaster.error).toHaveBeenCalled();
    });

    it('associates each capture limit with its own label and explanatory help', () => {
        for (const name of ['first', 'second']) {
            render(<ConfigDialog {...ringProps} open configName={name} onClose={vi.fn()} onSaved={vi.fn()} />);
        }
        const inputs = screen.getAllByRole('textbox', { name: 'Capture limit (pps)' });
        expect(new Set(inputs.map(input => input.id)).size).toBe(2);
        const helpIds = inputs.map(input => input.getAttribute('aria-describedby'));
        expect(new Set(helpIds).size).toBe(2);
        for (const input of inputs) {
            expect(input).toHaveAccessibleName('Capture limit (pps)');
            expect(input).toHaveAccessibleDescription(
                `Shared best-effort budget across all workers, up to ${PDUMP_RATE_PPS_MAX.toLocaleString()} pps. `
                + 'Burst: max(32, ceil(rate/1000)). Zero disables the limit; blank preserves it on update.',
            );
        }
    });
});
