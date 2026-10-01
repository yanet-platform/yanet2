import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ConfigStrip from './ConfigStrip';

describe('capture limit display', () => {
    afterEach(cleanup);
    it.each([
        { rate: undefined, visible: false },
        { rate: 0, visible: false },
        { rate: 1000, visible: true },
    ])('shows a limit only when it is enabled: $rate', ({ rate, visible }) => {
        render(<ConfigStrip config={{ name: 'capture', config: { rate_pps: rate } }} isCapturing={false} isCaptureActive={false} packetCount={0} ppsHistory={[]} onStartCapture={vi.fn()} onStopCapture={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} />);
        expect(screen.queryByText('Limit') !== null).toBe(visible);
    });

    it('marks large limits without displaying a rounded value as exact', () => {
        render(<ConfigStrip config={{ name: 'capture', config: { rate_pps: Number('18446744073709551615') } }} isCapturing={false} isCaptureActive={false} packetCount={0} ppsHistory={[]} onStartCapture={vi.fn()} onStopCapture={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} />);
        expect(screen.getByText(`> ${Number.MAX_SAFE_INTEGER} pps`)).toBeInTheDocument();
        expect(screen.queryByText('18446744073709552000 pps')).not.toBeInTheDocument();
    });
});
