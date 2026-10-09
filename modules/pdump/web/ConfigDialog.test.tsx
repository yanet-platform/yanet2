import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/react';
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
