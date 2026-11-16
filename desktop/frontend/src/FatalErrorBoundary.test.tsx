import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FatalErrorBoundary } from './FatalErrorBoundary';

function Broken(): never {
  throw new Error('broken render');
}

describe('FatalErrorBoundary', () => {
  it('reloads once after a fatal render error', () => {
    vi.useFakeTimers();
    const reload = vi.fn();
    render(<FatalErrorBoundary onReload={reload}><Broken /></FatalErrorBoundary>);
    expect(screen.getByText('Recovering AGX…')).toBeInTheDocument();
    vi.runAllTimers();
    expect(reload).toHaveBeenCalledOnce();
  });

  it('shows a manual recovery screen when automatic reload already ran', () => {
    sessionStorage.setItem('agx-frontend-recovery-at', String(Date.now()));
    render(<FatalErrorBoundary onReload={vi.fn()}><Broken /></FatalErrorBoundary>);
    expect(screen.getByText('AGX could not render this window')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reload interface' })).toBeInTheDocument();
  });
});

