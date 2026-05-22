/** Convert raw bytes in various formats to a number array. */
export const extractBytes = (data: string | Uint8Array | number[] | undefined): number[] | undefined => {
    if (!data) return undefined;
    if (Array.isArray(data)) return data;
    if (data instanceof Uint8Array) return Array.from(data);
    if (typeof data === 'string') {
        try {
            const binary = atob(data);
            return Array.from(binary, (c) => c.charCodeAt(0));
        } catch {
            return undefined;
        }
    }
    return undefined;
};
