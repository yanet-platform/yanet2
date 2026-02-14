import { useEffect } from 'react';

export interface UseDialogKeyboardShortcutOptions {
    /** Whether the dialog is open */
    open: boolean;
    /** Whether the form can be submitted */
    canSubmit: boolean;
    /** Callback to execute on Ctrl+Enter / Cmd+Enter */
    onConfirm: () => void;
}

/**
 * Hook that adds Ctrl+Enter / Cmd+Enter keyboard shortcut for dialog submission.
 *
 * @example
 * ```tsx
 * const MyDialog = ({ open, onConfirm }) => {
 *   const [canSubmit, setCanSubmit] = useState(false);
 *   const handleConfirm = useCallback(() => { ... }, []);
 *
 *   useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });
 *
 *   return <Dialog>...</Dialog>;
 * };
 * ```
 */
export const useDialogKeyboardShortcut = ({
    open,
    canSubmit,
    onConfirm,
}: UseDialogKeyboardShortcutOptions) => {
    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                if (!canSubmit) return;
                e.preventDefault();
                onConfirm();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, canSubmit, onConfirm]);
};
