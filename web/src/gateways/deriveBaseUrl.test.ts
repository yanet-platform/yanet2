import { describe, it, expect } from 'vitest';
import { deriveBaseUrl } from './GatewayDrawer';

describe('deriveBaseUrl', () => {
    it('returns empty string for an empty input', () => {
        expect(deriveBaseUrl('')).toBe('');
    });

    it('returns empty string for whitespace-only input', () => {
        expect(deriveBaseUrl('   ')).toBe('');
    });

    it('prepends http:// to a bare host:port', () => {
        expect(deriveBaseUrl('10.0.0.10:8080')).toBe('http://10.0.0.10:8080');
    });

    it('prepends http:// to a bare hostname', () => {
        expect(deriveBaseUrl('gateway-01')).toBe('http://gateway-01');
    });

    it('leaves an http:// URL unchanged', () => {
        expect(deriveBaseUrl('http://10.0.0.10:8080')).toBe('http://10.0.0.10:8080');
    });

    it('leaves an https:// URL unchanged', () => {
        expect(deriveBaseUrl('https://gateway.example.com')).toBe('https://gateway.example.com');
    });

    it('trims leading and trailing whitespace before processing', () => {
        expect(deriveBaseUrl('  10.0.0.1:9090  ')).toBe('http://10.0.0.1:9090');
    });
});
