import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { api } from '../../api';
import type { Project, ProjectCandidate } from '../../types';
import { ProjectView } from './ProjectView';

vi.mock('../../api', () => ({
  api: {
    ListProjectCandidates: vi.fn(),
    ValidateProjectDirectory: vi.fn(),
    RegisterProject: vi.fn(),
    HomeDirectory: vi.fn(),
    SelectProjectDirectory: vi.fn(),
    UpdateProject: vi.fn(),
    DeleteProject: vi.fn(),
  },
}));

const project: Project = {
  id: 'project-1',
  name: 'AGX',
  path: '/repo/agx',
  description: 'Desktop orchestration',
  accessGranted: true,
  taskCount: 3,
  activeCount: 1,
  waitingCount: 1,
  completeCount: 1,
  offlineCount: 0,
  createdAt: '2026-01-01T00:00:00.000Z',
  lastOpened: '2026-01-01T00:00:00.000Z',
};

const candidate: ProjectCandidate = {
  name: 'AGX',
  path: '/repo/agx',
  description: 'Desktop orchestration',
  isRegistered: false,
};

function renderProjects(projects: Project[] = [], onOpenProject = vi.fn()) {
  render(
    <ProjectView
      projects={projects}
      error=""
      candidateLimit={18}
      openProjectAfterAdd={false}
      onRefresh={vi.fn()}
      onOpenProject={onOpenProject}
      theme="dark"
      onToggleTheme={vi.fn()}
    />,
  );
  return onOpenProject;
}

describe('ProjectView', () => {
  it('renders project cards with task counts', () => {
    renderProjects([project]);

    expect(screen.getByText('AGX')).not.toBeNull();
    expect(screen.getByText('/repo/agx')).not.toBeNull();
    expect(screen.getAllByText((_, element) => element?.textContent?.includes('3 tasks') ?? false).length).toBeGreaterThan(0);
  });

  it('filters projects by name or path and opens the top result from search', async () => {
    const user = userEvent.setup();
    const otherProject = {
      ...project,
      id: 'project-2',
      name: 'Video Tools',
      path: '/repo/youtube-tools',
    };
    const onOpenProject = renderProjects([project, otherProject]);

    const search = screen.getByRole('textbox', { name: 'Search projects' });
    await user.type(search, 'video');

    expect(screen.queryByText('AGX')).toBeNull();
    expect(screen.getByText('Video Tools')).not.toBeNull();
    expect(screen.getByText('1 of 2 projects')).not.toBeNull();

    await user.keyboard('{Enter}');
    expect(onOpenProject).toHaveBeenCalledWith(otherProject);
  });

  it('moves down the project grid, reveals the card, and opens it with Enter', async () => {
    const user = userEvent.setup();
    const projects = Array.from({ length: 4 }, (_, index) => ({
      ...project,
      id: `project-${index + 1}`,
      name: `Project ${index + 1}`,
      path: `/repo/project-${index + 1}`,
    }));
    const onOpenProject = renderProjects(projects);
    const grid = document.querySelector<HTMLElement>('.project-grid')!;
    const firstCard = grid.querySelector<HTMLElement>('.project-card')!;
    Object.defineProperty(grid, 'clientWidth', { value: 600 });
    Object.defineProperty(firstCard, 'offsetWidth', { value: 280 });
    const computedStyle = vi.spyOn(window, 'getComputedStyle').mockReturnValue({ columnGap: '14px' } as CSSStyleDeclaration);
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView });

    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(document.activeElement?.getAttribute('data-grid-index')).toBe('2'));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest', inline: 'nearest' });

    await user.keyboard('{Enter}');
    expect(onOpenProject).toHaveBeenCalledWith(projects[2]);

    computedStyle.mockRestore();
    if (originalScrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: originalScrollIntoView });
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
    }
  });

  it('shows add-project validation failures inside the modal', async () => {
    const user = userEvent.setup();
    vi.mocked(api.ListProjectCandidates).mockResolvedValue([candidate]);
    vi.mocked(api.ValidateProjectDirectory).mockRejectedValue(new Error('not a git repository'));
    renderProjects();

    await user.click(screen.getByRole('button', { name: 'Add Project' }));
    await user.type(screen.getByPlaceholderText('Paste any Git repository path under your home directory'), '/tmp/nope');
    await user.click(screen.getByRole('button', { name: 'Use Path' }));

    await waitFor(() => expect(screen.getByText('not a git repository')).not.toBeNull());
  });

  it('keeps the delete confirmation open and shows cleanup errors when deletion fails', async () => {
    const user = userEvent.setup();
    vi.mocked(api.DeleteProject).mockRejectedValue(new Error('remove task worktree failed'));
    renderProjects([project]);

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getAllByRole('button', { name: 'Delete' }).at(-1)!);

    await waitFor(() => expect(screen.getByText('Error: remove task worktree failed')).not.toBeNull());
    expect(screen.getByRole('heading', { name: 'Delete Project' })).not.toBeNull();
  });
});
