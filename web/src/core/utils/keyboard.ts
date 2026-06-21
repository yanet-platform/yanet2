/** Returns true when the event target is a focusable text-entry element. */
export const isTypingTarget = (target: EventTarget | null): boolean => {
    const el = target as HTMLElement | null;
    return !!el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable);
};

/** Returns true when a modal or drawer overlay is currently open. */
export const isOverlayOpen = (): boolean =>
    document.querySelector('.g-modal, .yn-modal-backdrop, .yn-drawer--open') !== null;
