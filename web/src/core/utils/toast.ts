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
