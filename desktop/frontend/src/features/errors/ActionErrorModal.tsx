import { useEffect } from 'react';
import { X } from 'lucide-react';

import { IconButton } from '../../ui';

type ActionErrorModalProps = {
  title: string;
  message: string;
  onClose: () => void;
  primaryAction?: {
    label: string;
    onClick: () => void;
  };
};

export function ActionErrorModal({ title, message, onClose, primaryAction }: ActionErrorModalProps) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  return (
    <div className="modal-backdrop blurred" onMouseDown={onClose}>
      <section className="action-error-modal" role="alertdialog" aria-modal="true" aria-labelledby="action-error-title" onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-header">
          <div>
            <h2 id="action-error-title">Action Failed</h2>
            <p>{title}</p>
          </div>
          <IconButton label="Close" onClick={onClose}>
            <X size={18} />
          </IconButton>
        </header>
        <div className="modal-error">
          <span>{message}</span>
        </div>
        <footer className="wizard-actions">
          <button className="primary-button" onClick={primaryAction?.onClick ?? onClose}>{primaryAction?.label ?? 'OK'}</button>
        </footer>
      </section>
    </div>
  );
}
