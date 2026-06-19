import React from 'react';
import { render, cleanup } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useDrawerKeyboard } from './useDrawerKeyboard';

interface HarnessProps {
    open: boolean;
    onClose: () => void;
    onApply?: () => void;
    canApply?: boolean;
}

const Harness: React.FC<HarnessProps> = ({ open, onClose, onApply, canApply }) => {
    useDrawerKeyboard({ open, onClose, onApply, canApply });
    return open ? (
        <aside className="yn-drawer yn-drawer--open">
            <input data-testid="first" />
            <button type="button">ok</button>
        </aside>
    ) : null;
};

const press = (key: string, init: KeyboardEventInit = {}): void => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }));
};

afterEach(cleanup);

describe('useDrawerKeyboard', () => {
    it('closes on Escape while open', () => {
        const onClose = vi.fn();
        render(<Harness open onClose={onClose} />);
        press('Escape');
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does nothing on Escape while closed', () => {
        const onClose = vi.fn();
        render(<Harness open={false} onClose={onClose} />);
        press('Escape');
        expect(onClose).not.toHaveBeenCalled();
    });

    it('applies on Ctrl+Enter and Cmd+Enter when allowed', () => {
        const onApply = vi.fn();
        render(<Harness open onClose={vi.fn()} onApply={onApply} canApply />);
        press('Enter', { ctrlKey: true });
        press('Enter', { metaKey: true });
        expect(onApply).toHaveBeenCalledTimes(2);
    });

    it('does not apply on Ctrl+Enter when canApply is false', () => {
        const onApply = vi.fn();
        render(<Harness open onClose={vi.fn()} onApply={onApply} canApply={false} />);
        press('Enter', { ctrlKey: true });
        expect(onApply).not.toHaveBeenCalled();
    });

    it('ignores Ctrl+Enter when no onApply is provided', () => {
        const onClose = vi.fn();
        render(<Harness open onClose={onClose} />);
        press('Enter', { ctrlKey: true });
        expect(onClose).not.toHaveBeenCalled();
    });

    it('moves focus into the drawer on Tab from outside', () => {
        const { getByTestId } = render(<Harness open onClose={vi.fn()} />);
        expect(document.activeElement).toBe(document.body);
        press('Tab');
        expect(document.activeElement).toBe(getByTestId('first'));
    });

    it('leaves focus alone on Tab when already inside the drawer', () => {
        const { getByTestId } = render(<Harness open onClose={vi.fn()} onApply={vi.fn()} />);
        const button = document.querySelector('button');
        button?.focus();
        expect(document.activeElement).toBe(button);
        press('Tab');
        expect(document.activeElement).toBe(button);
        expect(document.activeElement).not.toBe(getByTestId('first'));
    });
});
