import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Agent, Project } from '../../types';
import { TaskCreateToolbar } from './TaskCreateToolbar';

const project: Project = {
  id: 'project-1',
  name: 'AGX',
  path: '/repo/agx',
  accessGranted: true,
  taskCount: 0,
  activeCount: 0,
  waitingCount: 0,
  completeCount: 0,
  offlineCount: 0,
  lastOpened: '2026-01-01T00:00:00Z',
  createdAt: '2026-01-01T00:00:00Z',
};

const agents: Agent[] = [
  { name: 'codex', command: 'codex', description: 'Codex', available: true },
  { name: 'claude', command: 'claude', description: 'Claude', available: false },
];

function renderToolbar(overrides?: Partial<React.ComponentProps<typeof TaskCreateToolbar>>) {
  const props = {
    project,
    title: 'ship it',
    description: '',
    agent: '',
    agents,
    allMighty: false,
    workspaceMode: 'worktree' as const,
    attachToDiscord: false,
    discordConnected: true,
    busy: false,
    grantingAccess: false,
    titleInputRef: createRef<HTMLInputElement>(),
    onTitleChange: vi.fn(),
    onDescriptionChange: vi.fn(),
    onAgentChange: vi.fn(),
    onAllMightyChange: vi.fn(),
    onWorkspaceModeChange: vi.fn(),
    onAttachToDiscordChange: vi.fn(),
    onCreate: vi.fn(),
    onQuickTemplate: vi.fn(),
    onGrantAccess: vi.fn(),
    ...overrides,
  };
  render(<TaskCreateToolbar {...props} />);
  return props;
}

describe('TaskCreateToolbar', () => {
  it('forwards create form edits and workspace mode choices', async () => {
    const user = userEvent.setup();
    const props = renderToolbar();

    await user.type(screen.getByPlaceholderText('Task title'), ' now');
    await user.click(screen.getByRole('button', { name: 'Project' }));
    await user.click(screen.getByLabelText('Agent'));
    await user.selectOptions(screen.getByLabelText('Agent'), 'codex');
    await user.click(screen.getByText('All-mighty'));

    expect(props.onTitleChange).toHaveBeenCalled();
    expect(props.onWorkspaceModeChange).toHaveBeenCalledWith('project');
    expect(props.onAgentChange).toHaveBeenCalledWith('codex');
    expect(props.onAllMightyChange).toHaveBeenCalledWith(true);
  });

  it('blocks Discord-attached creation until Discord is connected', () => {
    renderToolbar({ attachToDiscord: true, discordConnected: false });

    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Vanilla' })).toBeDisabled();
  });

  it('opens quick task template selection', async () => {
    const user = userEvent.setup();
    const props = renderToolbar();

    await user.click(screen.getByRole('button', { name: 'Vanilla' }));

    expect(props.onQuickTemplate).toHaveBeenCalledWith(expect.objectContaining({ id: 'vanilla' }));
  });
});
