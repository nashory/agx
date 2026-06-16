import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Agent } from '../../types';
import { QuickTaskModal } from './QuickTaskModal';

const agents: Agent[] = [
  { name: 'codex', command: 'codex', description: 'Codex agent', available: true },
  { name: 'missing', command: 'missing', description: 'Missing agent', available: false },
];

function renderModal(overrides?: Partial<React.ComponentProps<typeof QuickTaskModal>>) {
  const props = {
    template: { id: 'vanilla', label: 'Vanilla', title: 'main', prompt: '' },
    agents,
    busy: false,
    allMighty: false,
    initialWorkspaceMode: 'worktree' as const,
    initialAttachToDiscord: false,
    discordConnected: true,
    onCancel: vi.fn(),
    onCreate: vi.fn(),
    ...overrides,
  };
  render(<QuickTaskModal {...props} />);
  return props;
}

describe('QuickTaskModal', () => {
  it('creates with the selected agent, Discord attachment, and workspace mode', async () => {
    const user = userEvent.setup();
    const props = renderModal();

    await user.click(screen.getByText('Attach to Discord'));
    await user.click(screen.getByRole('button', { name: 'Project' }));
    await user.click(screen.getByRole('button', { name: /codex/i }));

    expect(props.onCreate).toHaveBeenCalledWith('codex', true, 'project');
  });

  it('disables Discord-attached choices when Discord is disconnected', () => {
    renderModal({ initialAttachToDiscord: true, discordConnected: false });

    expect(screen.getByText('Attach to Discord').closest('label')?.querySelector('input')).toBeDisabled();
    expect(screen.getByRole('button', { name: /Default agent/ })).toBeDisabled();
  });

  it('closes from escape and backdrop actions', async () => {
    const user = userEvent.setup();
    const props = renderModal();

    await user.keyboard('{Escape}');
    expect(props.onCancel).toHaveBeenCalledTimes(1);

    fireEvent.mouseDown(screen.getByText('Vanilla').closest('.modal-backdrop')!);
    expect(props.onCancel).toHaveBeenCalledTimes(2);
  });
});
