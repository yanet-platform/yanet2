import { describe, it, expect, vi, beforeEach } from 'vitest';

const addMock = vi.fn();

vi.mock('@gravity-ui/uikit/toaster-singleton', () => ({
    toaster: {
        add: (options: { content: string }) => addMock(options),
    },
}));

import { warnConfigsUnknown } from './toast';

const namesOfLength = (count: number): string[] =>
    Array.from({ length: count }, (_unused, idx) => `cfg-${String.fromCharCode(97 + idx)}`);

describe('warnConfigsUnknown', () => {
    beforeEach(() => {
        addMock.mockClear();
    });

    it('lists a bounded prefix and mentions the total and remainder counts when over the cap', () => {
        const names = namesOfLength(9);
        warnConfigsUnknown('key', 'route')(names);

        const content = addMock.mock.calls[0][0].content as string;
        const listedCount = names.filter((name) => content.includes(name)).length;

        expect(listedCount).toBeLessThan(names.length);
        expect(content).toContain(String(names.length));
        expect(content).toContain(String(names.length - listedCount));
    });

    it('lists the same number of names regardless of how far over the cap the list is', () => {
        const shortOverflow = namesOfLength(6);
        const longOverflow = namesOfLength(9);

        warnConfigsUnknown('key', 'route')(shortOverflow);
        const shortContent = addMock.mock.calls[0][0].content as string;
        const shortListed = shortOverflow.filter((name) => shortContent.includes(name)).length;

        addMock.mockClear();

        warnConfigsUnknown('key', 'route')(longOverflow);
        const longContent = addMock.mock.calls[0][0].content as string;
        const longListed = longOverflow.filter((name) => longContent.includes(name)).length;

        expect(longListed).toBe(shortListed);
    });

    it('lists all names and mentions no remainder count when the list is exactly at the cap', () => {
        const names = namesOfLength(5);
        warnConfigsUnknown('key', 'route')(names);

        const content = addMock.mock.calls[0][0].content as string;

        for (const name of names) {
            expect(content).toContain(name);
        }

        const numbers = content.match(/\d+/g) ?? [];
        for (const number of numbers) {
            expect(Number(number)).toBe(names.length);
        }
    });
});
