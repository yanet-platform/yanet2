import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { pdumpApi } from '@yanet/core/api/pdump';
import { toaster } from '@yanet/core/utils';
import { ConfigDialog } from './ConfigDialog';

vi.mock('@yanet/core/utils', () => ({
    toaster: { error: vi.fn(), success: vi.fn() },
}));

describe('capture limit editing', () => {
    afterEach(cleanup);
    beforeEach(() => {
        vi.restoreAllMocks();
        vi.clearAllMocks();
        vi.spyOn(pdumpApi, 'setConfig').mockResolvedValue(undefined);
    });

    it.each([0, 1000, Number.MAX_SAFE_INTEGER])('saves the exact numeric limit %s', async rate => {
        render(<ConfigDialog open configName="capture" initialConfig={{ filter: 'udp', mode: 1, rate_pps: rate }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ rate_pps: rate })));
    });

    it('preserves a large existing limit when saving another field', async () => {
        render(<ConfigDialog open configName="capture" initialConfig={{ filter: 'udp', mode: 1, rate_pps: Number('18446744073709551615') }} onClose={vi.fn()} onSaved={vi.fn()} />);
        expect(screen.getByPlaceholderText('Keep existing limit')).toHaveValue('');
        fireEvent.change(screen.getByPlaceholderText('tcp port 80'), { target: { value: 'tcp' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ filter: 'tcp', rate_pps: undefined })));
    });

    it('allows explicitly disabling a large existing limit', async () => {
        render(<ConfigDialog open configName="capture" initialConfig={{ filter: 'udp', mode: 1, rate_pps: Number('18446744073709551615') }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(screen.getByPlaceholderText('Keep existing limit'), { target: { value: '0' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(pdumpApi.setConfig).toHaveBeenCalledWith('capture', expect.objectContaining({ rate_pps: 0 })));
    });

    it.each(['9007199254740992', '18446744073709551615', '-1', '1.5', '1e3'])('rejects inexact or invalid input %s before sending', value => {
        render(<ConfigDialog open configName="capture" initialConfig={{ filter: 'udp', mode: 1 }} onClose={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(screen.getByPlaceholderText('0'), { target: { value } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(pdumpApi.setConfig).not.toHaveBeenCalled();
        expect(toaster.error).toHaveBeenCalled();
    });
});
