/**
 * Copies text to the clipboard.
 *
 * Tries the modern Clipboard API first (requires a secure context). Falls back
 * to the legacy execCommand('copy') path so the feature works on plain-HTTP
 * LAN deployments where navigator.clipboard is unavailable.
 *
 * Throws an Error with a descriptive message when both paths fail.
 */
export async function copyToClipboard(text: string): Promise<void> {
    if (window.isSecureContext && typeof navigator.clipboard?.writeText === 'function') {
        try {
            await navigator.clipboard.writeText(text);
            return;
        } catch (err) {
            // Fall through to the legacy path.
            void err;
        }
    }

    // Legacy fallback: create a hidden textarea, select its contents, and use
    // execCommand('copy'). Works on plain HTTP contexts.
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none;';
    document.body.appendChild(ta);
    try {
        ta.focus();
        ta.select();
        const ok = document.execCommand('copy');
        if (!ok) {
            throw new Error('execCommand copy returned false');
        }
    } finally {
        document.body.removeChild(ta);
    }
}
