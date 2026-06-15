import { describe, expect, it, vi } from 'vitest';

import {
  agentLabel,
  clampNumber,
  clampZoomLevel,
  defaultPreferences,
  discordSyncLabel,
  discordSyncTime,
  humanizeErrorMessage,
  loadPreferences,
  preferenceKey,
  statusClass,
  statusLabel,
  taskInterfaceCounts,
  tasksForInterfaceFilter,
  type TaskInterfaceFilter,
} from './appLogic';
import type { Task } from './types';

function task(id: string, iface: Task['interface'], createdAt = `2026-01-01T00:00:0${id}.000Z`): Task {
  return {
    id,
    projectId: 'project-1',
    title: `Task ${id}`,
    interface: iface,
    workspaceMode: 'worktree',
    status: 'waiting',
    agent: 'codex',
    allMighty: false,
    createdAt,
    updatedAt: createdAt,
  };
}

describe('appLogic', () => {
  it('maps project-mode task conflicts to actionable user copy', () => {
    const message = humanizeErrorMessage(
      'runtime API POST /v1/tasks failed: 409 Conflict: another project-mode task is already active for this project: task-123',
    );

    expect(message).toBe(
      'Another project-mode task is already active for this project. Stop task task-123 or choose Worktree mode before creating a new task.',
    );
  });

  it('loads preferences with defaults and numeric clamps', () => {
    localStorage.setItem(preferenceKey, JSON.stringify({
      showActionLog: false,
      defaultTaskView: 'list',
      monitorRefreshSeconds: 999,
      projectCandidateLimit: '2',
    }));

    expect(loadPreferences()).toEqual({
      ...defaultPreferences,
      showActionLog: false,
      defaultTaskView: 'list',
      monitorRefreshSeconds: 60,
      projectCandidateLimit: 6,
    });
  });

  it('falls back to defaults when stored preferences are invalid JSON', () => {
    localStorage.setItem(preferenceKey, '{invalid');

    expect(loadPreferences()).toEqual(defaultPreferences);
  });

  it('clamps numeric settings and zoom levels', () => {
    expect(clampNumber('8.6', 2, 60, 5)).toBe(9);
    expect(clampNumber('nope', 2, 60, 5)).toBe(5);
    expect(clampZoomLevel(0.71)).toBe(0.8);
    expect(clampZoomLevel(1.46)).toBe(1.5);
  });

  it('formats Discord sync status and relative sync time', () => {
    vi.setSystemTime(new Date('2026-01-01T00:10:00.000Z'));

    expect(discordSyncLabel()).toBe('Sync not started');
    expect(discordSyncLabel({
      status: 'failed',
      attempts: 2,
      lastFailureAt: '2026-01-01T00:05:00.000Z',
      updatedAt: '2026-01-01T00:04:00.000Z',
    })).toBe('Sync failed');
    expect(discordSyncTime({
      status: 'synced',
      attempts: 1,
      lastSuccessAt: '2026-01-01T00:09:30.000Z',
      updatedAt: '2026-01-01T00:09:00.000Z',
    })).toBe('just now');

    vi.useRealTimers();
  });

  it('keeps status and agent labels stable', () => {
    expect(statusLabel('active')).toBe('⚡ active');
    expect(statusClass('unknown-status')).toBe('unknown');
    expect(agentLabel('codex')).toBe('Codex');
    expect(agentLabel('')).toBe('Default agent');
  });

  it('counts and orders task interface filters', () => {
    const tasks = [task('1', 'discord'), task('2', 'local'), task('3', 'discord')];

    expect(taskInterfaceCounts(tasks)).toEqual({
      all: 3,
      desktop: 1,
      discord: 2,
    } satisfies Record<TaskInterfaceFilter, number>);
    expect(tasksForInterfaceFilter(tasks, 'all').map((item) => item.id)).toEqual(['2', '1', '3']);
    expect(tasksForInterfaceFilter(tasks, 'desktop').map((item) => item.id)).toEqual(['2']);
    expect(tasksForInterfaceFilter(tasks, 'discord').map((item) => item.id)).toEqual(['1', '3']);
  });
});
