import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';

import type { MonitorTask } from '../../api';
import type { Project } from '../../types';
import { MonitorView } from './MonitorView';

const project: Project = {
  id: 'project-1',
  name: 'AGX',
  path: '/repo/agx',
  accessGranted: true,
  taskCount: 1,
  activeCount: 1,
  waitingCount: 0,
  completeCount: 0,
  offlineCount: 0,
  createdAt: '2026-01-01T00:00:00.000Z',
  lastOpened: '2026-01-01T00:00:00.000Z',
};

const task: MonitorTask = {
  id: 'task-1',
  projectId: 'project-1',
  projectName: 'AGX',
  projectPath: '/repo/agx',
  title: 'Fix Discord sync',
  description: 'Keep retry visible',
  interface: 'discord',
  workspaceMode: 'worktree',
  status: 'active',
  agent: 'codex',
  allMighty: true,
  sessionName: 'agx-task-1',
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-01-01T00:05:00.000Z',
};

function renderMonitor(tasks: MonitorTask[] = [], overrides: Partial<ComponentProps<typeof MonitorView>> = {}) {
  const props = {
    tasks,
    projects: [project],
    error: '',
    refreshSeconds: 5,
    busy: false,
    onRefresh: vi.fn(),
    onDeleteTask: vi.fn(),
    onClearStaleTasks: vi.fn(),
    onOpenWorkspace: vi.fn(),
    theme: 'dark' as const,
    onToggleTheme: vi.fn(),
    nowMs: Date.parse('2026-01-03T01:05:00.000Z'),
    ...overrides,
  };
  render(
    <MonitorView {...props} />,
  );
  return props;
}

describe('MonitorView', () => {
  it('renders the empty state when no tasks exist', () => {
    renderMonitor();

    expect(screen.getByText('No tasks')).not.toBeNull();
    expect(screen.getByText('Create a task from Workspace to start monitoring agent state.')).not.toBeNull();
  });

  it('renders populated task rows with status and project metadata', () => {
    renderMonitor([task]);

    expect(screen.getByText('Fix Discord sync')).not.toBeNull();
    expect(screen.getByText('⚡ active')).not.toBeNull();
    expect(screen.getByText('agx-task-1')).not.toBeNull();
    expect(screen.getByText('/repo/agx')).not.toBeNull();
    expect(screen.getByText('Lifetime')).not.toBeNull();
    expect(screen.getByText('2d 1h')).not.toBeNull();
  });

  it('marks stale sessions and exposes stale cleanup actions', () => {
    const onDeleteTask = vi.fn();
    const onClearStaleTasks = vi.fn();
    renderMonitor([task], { onDeleteTask, onClearStaleTasks });

    expect(screen.getAllByText('Stale').length).toBeGreaterThan(0);
    expect(screen.getByText('2d 1h ago')).not.toBeNull();

    fireEvent.click(screen.getByText('Clear stale'));
    expect(onClearStaleTasks).toHaveBeenCalledWith([task]);

    fireEvent.click(screen.getByText('Delete'));
    expect(onDeleteTask).toHaveBeenCalledWith(task);
  });
});
