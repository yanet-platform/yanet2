import { useEffect } from 'react';

export interface UseDialogKeyboardShortcutOptions {
    /** Whether the dialog is open */
    open: boolean;
    /** Whether the form can be submitted */
    canSubmit: boolean;
    /** Callback to execute on Ctrl+Enter / Cmd+Enter */
    onConfirm: () => void;
    /** Optional callback to execute on Escape. When provided and the dialog is open, pressing Escape calls this and prevents default browser handling. */
    onCancel?: () => void;
}

/**
 * Hook that adds Ctrl+Enter / Cmd+Enter keyboard shortcut for dialog submission,
 * and optionally wires Escape to a cancel callback.
 *
 * @example
 * ```tsx
 * const MyDialog = ({ open, onConfirm, onClose }) => {
 *   const [canSubmit, setCanSubmit] = useState(false);
 *   const handleConfirm = useCallback(() => { ... }, []);
 *
 *   useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm, onCancel: onClose });
 *
 *   return <Dialog>...</Dialog>;
 * };
 * ```
 */
export const useDialogKeyboardShortcut = ({
    open,
    canSubmit,
    onConfirm,
    onCancel,
}: UseDialogKeyboardShortcutOptions) => {
    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                if (!canSubmit) return;
                e.preventDefault();
                e.stopPropagation();
                onConfirm();
            } else if (e.key === 'Escape' && onCancel) {
                e.preventDefault();
                e.stopPropagation();
                onCancel();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, canSubmit, onConfirm, onCancel]);
};
