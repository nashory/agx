import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { ActionErrorModal } from './ActionErrorModal';

describe('ActionErrorModal', () => {
  it('renders the action title and closes from explicit, backdrop, and escape actions', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = render(<ActionErrorModal title="Create task" message="runtime conflict" onClose={onClose} />);

    expect(screen.getByRole('alertdialog')).not.toBeNull();
    expect(screen.getByText('Create task')).not.toBeNull();
    expect(screen.getByText('runtime conflict')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'OK' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    rerender(<ActionErrorModal title="Create task" message="runtime conflict" onClose={onClose} />);
    fireEvent.mouseDown(screen.getByRole('alertdialog').parentElement!);
    expect(onClose).toHaveBeenCalledTimes(2);

    rerender(<ActionErrorModal title="Create task" message="runtime conflict" onClose={onClose} />);
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it('runs the primary action without implicitly closing the modal', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onRetry = vi.fn();
    render(<ActionErrorModal title="Sync Discord" message="timeout" onClose={onClose} primaryAction={{ label: 'Retry', onClick: onRetry }} />);

    await user.click(screen.getByRole('button', { name: 'Retry' }));

    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
  });
});
