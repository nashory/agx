import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Project } from '../../types';
import { GrantAccessModal } from './GrantAccessModal';

const project: Project = {
  id: 'project-1',
  name: 'AGX',
  path: '/repo/agx',
  accessGranted: false,
  accessError: 'permission denied',
  taskCount: 0,
  activeCount: 0,
  waitingCount: 0,
  completeCount: 0,
  offlineCount: 0,
  lastOpened: '2026-01-01T00:00:00Z',
  createdAt: '2026-01-01T00:00:00Z',
};

describe('GrantAccessModal', () => {
  it('shows project access details and forwards actions', async () => {
    const user = userEvent.setup();
    const onBack = vi.fn();
    const onGrant = vi.fn();
    render(<GrantAccessModal project={project} granting={false} onBack={onBack} onGrant={onGrant} />);

    expect(screen.getByRole('dialog')).not.toBeNull();
    expect(screen.getByText('/repo/agx')).not.toBeNull();
    expect(screen.getByText('permission denied')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'Grant Access' }));
    await user.click(screen.getByRole('button', { name: 'Back' }));

    expect(onGrant).toHaveBeenCalledTimes(1);
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it('disables actions while granting', () => {
    render(<GrantAccessModal project={project} granting={true} onBack={vi.fn()} onGrant={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Granting...' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();
  });
});
