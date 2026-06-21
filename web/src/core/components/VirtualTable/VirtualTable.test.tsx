import { describe, it, expect } from 'vitest';
import { isInteractiveTarget } from './VirtualTable';

const el = (html: string, selector: string): Element => {
    const host = document.createElement('div');
    host.innerHTML = html;
    const found = host.querySelector(selector);
    if (!found) throw new Error(`no element matched ${selector}`);
    return found;
};

describe('isInteractiveTarget', () => {
    it('returns false for null', () => {
        expect(isInteractiveTarget(null)).toBe(false);
    });

    it('returns false for a plain text cell', () => {
        expect(isInteractiveTarget(el('<span>Alpha</span>', 'span'))).toBe(false);
    });

    it('returns true for a button', () => {
        expect(isInteractiveTarget(el('<button>toggle</button>', 'button'))).toBe(true);
    });

    it('returns true for an element nested inside a button', () => {
        expect(isInteractiveTarget(el('<button><svg><title>icon</title></svg></button>', 'title'))).toBe(true);
    });

    it('returns true for a link with href', () => {
        expect(isInteractiveTarget(el('<a href="/x">link</a>', 'a'))).toBe(true);
    });

    it('returns false for a link without href', () => {
        expect(isInteractiveTarget(el('<a>anchor</a>', 'a'))).toBe(false);
    });

    it('returns true for a role=button element', () => {
        expect(isInteractiveTarget(el('<div role="button">x</div>', '[role="button"]'))).toBe(true);
    });
});
