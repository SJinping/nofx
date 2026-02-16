import { useEffect, useCallback } from 'react';
import { createPortal } from 'react-dom';

interface ModalProps {
  isOpen: boolean;
  onClose: () => void;
  title?: string;
  children: React.ReactNode;
}

export function Modal({ isOpen, onClose, title, children }: ModalProps) {
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    },
    [onClose]
  );

  useEffect(() => {
    if (isOpen) {
      document.addEventListener('keydown', handleKeyDown);
      document.body.style.overflow = 'hidden';
    }
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      document.body.style.overflow = '';
    };
  }, [isOpen, handleKeyDown]);

  if (!isOpen) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-[9999] flex items-center justify-center"
      style={{ background: 'rgba(0, 0, 0, 0.75)', backdropFilter: 'blur(4px)' }}
      onClick={onClose}
    >
      <div
        className="relative rounded-lg overflow-hidden animate-scale-in"
        style={{
          background: '#1E2329',
          border: '1px solid #2B3139',
          boxShadow: '0 25px 50px rgba(0, 0, 0, 0.5)',
          width: '80vw',
          maxWidth: '1200px',
          maxHeight: '85vh',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-6 py-4"
          style={{ borderBottom: '1px solid #2B3139' }}
        >
          {title && (
            <h3 className="text-lg font-bold" style={{ color: '#EAECEF' }}>
              {title}
            </h3>
          )}
          <button
            onClick={onClose}
            className="ml-auto w-8 h-8 flex items-center justify-center rounded transition-colors"
            style={{ color: '#848E9C' }}
            onMouseEnter={(e) => {
              e.currentTarget.style.background = '#2B3139';
              e.currentTarget.style.color = '#EAECEF';
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.background = 'transparent';
              e.currentTarget.style.color = '#848E9C';
            }}
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
              <path d="M3.72 3.72a.75.75 0 011.06 0L8 6.94l3.22-3.22a.75.75 0 111.06 1.06L9.06 8l3.22 3.22a.75.75 0 11-1.06 1.06L8 9.06l-3.22 3.22a.75.75 0 01-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 010-1.06z" />
            </svg>
          </button>
        </div>

        {/* Body */}
        <div className="p-6 overflow-y-auto" style={{ maxHeight: 'calc(85vh - 65px)' }}>
          {children}
        </div>
      </div>
    </div>,
    document.body
  );
}
