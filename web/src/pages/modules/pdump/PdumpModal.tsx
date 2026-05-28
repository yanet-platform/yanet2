import React, { useEffect } from 'react';
import './pdump.scss';

interface PdumpModalProps {
    title: string;
    width?: string;
    onClose: () => void;
    children: React.ReactNode;
    footer: React.ReactNode;
}

/** Shared modal chrome for pdump dialogs (config + delete). */
const PdumpModal: React.FC<PdumpModalProps> = ({ title, width = '620px', onClose, children, footer }) => {
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onClose();
        };
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [onClose]);

    return (
        <div className="pdump-modal-backdrop" onClick={onClose}>
            <div
                className="pdump-modal"
                style={{ width }}
                onClick={e => e.stopPropagation()}
            >
                <div className="pdump-modal__header">
                    <div className="pdump-modal__title">{title}</div>
                    <button type="button" className="fw-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                </div>
                <div className="pdump-modal__body">
                    {children}
                </div>
                <div className="pdump-modal__footer">
                    {footer}
                </div>
            </div>
        </div>
    );
};

export default PdumpModal;
