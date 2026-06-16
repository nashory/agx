import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Task } from '../../types';
import { TaskCard, TaskList } from './TaskBoardItems';

const task: Task = {
  id: 'task-1',
  projectId: 'project-1',
  title: 'Ship it',
  interface: 'local',
  workspaceMode: 'worktree',
  status: 'offline',
  agent: 'codex',
  allMighty: true,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

describe('TaskBoardItems', () => {
  it('opens and focuses task cards', async () => {
    const user = userEvent.setup();
    const onOpen = vi.fn();
    const onFocus = vi.fn();
    render(<TaskCard task={task} busy={false} focused={false} onFocus={onFocus} onOpen={onOpen} onAction={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: /Ship it/ }));

    expect(onFocus).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('renames tasks through the action callback', async () => {
    const user = userEvent.setup();
    const onAction = vi.fn();
    render(<TaskCard task={task} busy={false} focused={false} onFocus={vi.fn()} onOpen={vi.fn()} onAction={onAction} />);

    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.clear(screen.getByLabelText('Task name'));
    await user.type(screen.getByLabelText('Task name'), 'Review');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(onAction).toHaveBeenCalledWith(expect.any(Function), 'rename task "Ship it" to "Review"');
  });

  it('selects rows in list mode', async () => {
    const user = userEvent.setup();
    const onFocusTask = vi.fn();
    const onSelectTask = vi.fn();
    render(<TaskList tasks={[task]} busy={false} focusedTaskID={null} onFocusTask={onFocusTask} onSelectTask={onSelectTask} onAction={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: 'Ship it' }));

    expect(onFocusTask).toHaveBeenCalledWith('task-1');
    expect(onSelectTask).toHaveBeenCalledWith(task);
  });
});
