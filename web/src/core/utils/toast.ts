import { toaster as gravityToaster } from '@gravity-ui/uikit/toaster-singleton';

/**
 * Custom toaster utility with predefined methods for different notification types
 */
export const toaster = {
    /**
     * Show success toast notification
     * @param name - Unique identifier for the toast
     * @param message - Success message
     */
    success: (name: string, message: string): void => {
        gravityToaster.add({
            name,
            title: 'Success',
            content: message,
            theme: 'success',
            isClosable: true,
            autoHiding: 3000,
        });
    },

    /**
     * Show info toast notification
     * @param name - Unique identifier for the toast
     * @param message - Info message
     * @param title - Info title (default: 'Info')
     */
    info: (name: string, message: string, title: string = 'Info'): void => {
        gravityToaster.add({
            name,
            title,
            content: message,
            theme: 'info',
            isClosable: true,
            autoHiding: 3000,
        });
    },

    /**
     * Show warning toast notification
     * @param name - Unique identifier for the toast
     * @param message - Warning message
     * @param title - Warning title (default: 'Warning')
     */
    warning: (name: string, message: string, title: string = 'Warning'): void => {
        gravityToaster.add({
            name,
            title,
            content: message,
            theme: 'warning',
            isClosable: true,
            autoHiding: 3000,
        });
    },

    /**
     * Show error toast notification
     * @param name - Unique identifier for the toast
     * @param message - Error message
     * @param error - Optional error (any type, will be converted to string)
     */
    error: (name: string, message: string, error?: unknown): void => {
        const errorMessage = error instanceof Error ? error.message : String(error || 'Unknown error');
        gravityToaster.add({
            name,
            title: 'Error',
            content: `${message}: ${errorMessage}`,
            theme: 'danger',
            isClosable: true,
            autoHiding: 5000,
        });
    },
};

/** Names beyond this count are summarised instead of listed, so a control plane
 * that lost a dozen configurations cannot produce an unbounded toast string.
 */
const MAX_LISTED_NAMES = 5;

/**
 * Build an `onDropped` callback for `loadKnownConfigs` that warns about unknown configs.
 *
 * The helper owns the message and the name list, so a caller only needs to supply the
 * toast dedup key and the subject word used in the message. It fires when the control
 * plane does not know at least one of the requested configs; a remount fires it again,
 * and it is the toast dedup key, not the callback, that collapses the repeat.
 */
export const warnConfigsUnknown = (toastKey: string, subject: string) => (names: string[]): void => {
    const [noun, pronoun] = names.length === 1 ? ['configuration', 'It is'] : ['configurations', 'They are'];
    const listed = names.slice(0, MAX_LISTED_NAMES);
    const rest = names.length - listed.length;
    const list = rest > 0
        ? `${listed.join(', ')}, and ${rest} more`
        : listed.join(', ');
    toaster.warning(
        toastKey,
        `The control plane does not know ${names.length} ${subject} ${noun} (${list}). ${pronoun} not shown here.`,
    );
};
