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

function renderProjects(projects: Project[] = []) {
  render(
    <ProjectView
      projects={projects}
      error=""
      candidateLimit={18}
      openProjectAfterAdd={false}
      onRefresh={vi.fn()}
      onOpenProject={vi.fn()}
      theme="dark"
      onToggleTheme={vi.fn()}
    />,
  );
}

describe('ProjectView', () => {
  it('renders project cards with task counts', () => {
    renderProjects([project]);

    expect(screen.getByText('AGX')).not.toBeNull();
    expect(screen.getByText('/repo/agx')).not.toBeNull();
    expect(screen.getAllByText((_, element) => element?.textContent?.includes('3 tasks') ?? false).length).toBeGreaterThan(0);
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
});
