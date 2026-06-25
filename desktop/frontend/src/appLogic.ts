import type { Task, TaskDiscordSync, TaskStatus, ViewMode } from './types';
import type { ThemeMode } from './ui';

export type MainTab = 'workspace' | 'monitor' | 'discord' | 'shortcuts' | 'settings';
export type TaskInterfaceFilter = 'all' | 'desktop' | 'discord';
export const mainTabs: MainTab[] = ['workspace', 'monitor', 'discord', 'shortcuts', 'settings'];

export type DesktopActionResult = {
  taskID?: string;
  expectSession?: boolean;
};
export type DesktopAction = (action: () => Promise<DesktopActionResult | void>, label?: string) => Promise<boolean>;

export type UserPreferences = {
  showActionLog: boolean;
  openProjectAfterAdd: boolean;
  defaultTaskView: ViewMode;
  monitorRefreshSeconds: number;
  projectCandidateLimit: number;
  defaultAllMighty: boolean;
};

export type QuickTaskTemplate = {
  id: string;
  title: string;
  label: string;
  prompt: string;
};

export const mainQuickTaskPrompt = `Read the repository context files first and use them to understand this project before doing any work.

Start with AGENTS.md, CLAUDE.md, README.md, and any other repo-local context or instruction files that are present. Summarize the relevant project conventions to yourself, then wait for the user's next instruction.`;

export const quickTaskTemplates: QuickTaskTemplate[] = [
  {
    id: 'vanilla',
    title: 'Vanilla',
    label: 'Vanilla',
    prompt: '',
  },
  {
    id: 'coding-machine',
    title: 'Coding Machine',
    label: 'Coding Machine',
    prompt: `You are a senior software engineer working in this repository. Build production-quality code with a bias for simple, maintainable designs that fit the existing architecture.

For future user requests, start by inspecting the repository structure, tests, and nearby implementation patterns. Then identify the concrete change needed, implement it end to end, and run the most relevant checks. Keep edits focused, avoid unrelated refactors, and preserve existing behavior unless the task clearly requires changing it.

When you finish requested work, summarize what changed, what you verified, and any remaining risks or follow-up work.

Do not start inspecting files or making changes yet. Wait for the user's next instruction.`,
  },
  {
    id: 'code-reviewer',
    title: 'Code Reviewer',
    label: 'Code Reviewer',
    prompt: `You are a rigorous senior code reviewer for this repository. For future review requests, prioritize correctness, regressions, missing tests, security, data loss, concurrency issues, and UX-breaking behavior.

Do not rewrite code unless a small fix is necessary to prove or unblock the review. Lead with concrete findings ordered by severity, each grounded in file and line references. If there are no blocking findings, say that clearly and call out the main residual risks or test gaps.

Do not start reviewing files yet. Wait for the user's next instruction.`,
  },
  {
    id: 'planner',
    title: 'Planner',
    label: 'Planner',
    prompt: `You are a senior technical planner. For future planning requests, analyze this repository and produce practical implementation plans without making code edits unless explicitly asked.

Read enough code to understand the real boundaries, data flow, and tests. Propose focused plans with tradeoffs, risks, validation steps, and the files or modules likely to change. Prefer incremental steps that keep the app working throughout the change.

Do not start planning or inspecting files yet. Wait for the user's next instruction.`,
  },
];

export const defaultPreferences: UserPreferences = {
  showActionLog: true,
  openProjectAfterAdd: true,
  defaultTaskView: 'grid',
  monitorRefreshSeconds: 5,
  projectCandidateLimit: 18,
  defaultAllMighty: false,
};

export const preferenceKey = 'agx-preferences';
export const zoomPreferenceKey = 'agx-desktop-zoom';
export const defaultZoomLevel = 1;
const minZoomLevel = 0.8;
const maxZoomLevel = 1.5;
export const zoomStep = 0.1;

export function loadPreferences(): UserPreferences {
  try {
    const stored = JSON.parse(localStorage.getItem(preferenceKey) ?? '{}') as Partial<UserPreferences>;
    return {
      showActionLog: stored.showActionLog ?? defaultPreferences.showActionLog,
      openProjectAfterAdd: stored.openProjectAfterAdd ?? defaultPreferences.openProjectAfterAdd,
      defaultTaskView: stored.defaultTaskView === 'list' ? 'list' : 'grid',
      monitorRefreshSeconds: clampNumber(stored.monitorRefreshSeconds, 2, 60, defaultPreferences.monitorRefreshSeconds),
      projectCandidateLimit: clampNumber(stored.projectCandidateLimit, 6, 50, defaultPreferences.projectCandidateLimit),
      defaultAllMighty: stored.defaultAllMighty ?? defaultPreferences.defaultAllMighty,
    };
  } catch {
    return defaultPreferences;
  }
}

export function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(parsed)) return fallback;
  return Math.min(max, Math.max(min, Math.round(parsed)));
}

export function loadZoomLevel(): number {
  const parsed = Number(localStorage.getItem(zoomPreferenceKey));
  return clampZoomLevel(Number.isFinite(parsed) ? parsed : defaultZoomLevel);
}

export function clampZoomLevel(value: number): number {
  return Math.min(maxZoomLevel, Math.max(minZoomLevel, Math.round(value * 10) / 10));
}

export function terminalTheme(theme: ThemeMode) {
  return theme === 'light'
    ? {
        background: '#f8fafc',
        foreground: '#172033',
        cursor: '#3867d6',
        selectionBackground: '#dbe7ff',
      }
    : {
        background: '#101521',
        foreground: '#dce3ef',
        cursor: '#8bbcff',
        selectionBackground: '#263956',
      };
}

export function isTextEntry(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  return Boolean(element?.closest('button, input, textarea, select, a, [contenteditable="true"]'));
}

export function isTerminalInput(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  return Boolean(element?.closest('.terminal-host, .task-output-terminal, .split-terminal'));
}

export function focusSidebarNavigation() {
  const activeButton = document.querySelector<HTMLElement>('.sidebar-button.active');
  if (activeButton) {
    activeButton.focus();
    return;
  }
  document.querySelector<HTMLElement>('.sidebar-nav')?.focus();
}

export function focusMainContent() {
  requestAnimationFrame(() => document.querySelector<HTMLElement>('.app-content')?.focus());
}

export function projectGridColumns(grid: HTMLElement | null): number {
  if (!grid) return 1;
  const firstCard = grid.querySelector<HTMLElement>('.project-card, .task-card');
  if (!firstCard) return 1;
  const gap = Number.parseFloat(window.getComputedStyle(grid).columnGap) || 0;
  return Math.max(1, Math.floor((grid.clientWidth + gap) / (firstCard.offsetWidth + gap)));
}

export function errorMessage(err: unknown): string {
  return humanizeErrorMessage(err instanceof Error ? err.message : String(err));
}

export function humanizeErrorMessage(message: string): string {
  const cleaned = message.replace(/^runtime API [A-Z]+ [^ ]+ failed: \d{3} [^:]+: /, '').trim();
  const activeProjectTask = cleaned.match(/another project-mode task is already active for this project: ([\w-]+)/i);
  if (activeProjectTask) {
    return `Another project-mode task is already active for this project. Stop task ${activeProjectTask[1]} or choose Worktree mode before creating a new task.`;
  }
  return cleaned || message;
}

export function isAgentContextClearCommand(message: string): boolean {
  return message.trim().toLowerCase() === '/clear';
}

export function timestamp(): string {
  return new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function relativeTime(value: string): string {
  const timestampMs = new Date(value).getTime();
  if (!Number.isFinite(timestampMs)) return 'unknown';
  const seconds = Math.max(0, Math.floor((Date.now() - timestampMs) / 1000));
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} hr ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} day${days === 1 ? '' : 's'} ago`;
  return new Date(value).toLocaleDateString([], { month: 'short', day: 'numeric' });
}

export function discordSyncLabel(sync?: TaskDiscordSync): string {
  if (!sync) return 'Sync not started';
  if (sync.status === 'synced') return 'Synced';
  if (sync.status === 'failed') return 'Sync failed';
  return 'Sync pending';
}

export function discordSyncTime(sync?: TaskDiscordSync): string {
  if (!sync) return '';
  const timestamp = sync.lastFailureAt || sync.lastSuccessAt || sync.updatedAt;
  return timestamp ? relativeTime(timestamp) : '';
}

export function isTaskStatus(value: string): value is TaskStatus {
  return value === 'active' || value === 'waiting' || value === 'complete' || value === 'offline';
}

export function statusLabel(status?: string): string {
  switch (status) {
    case 'active':
      return '⚡ active';
    case 'waiting':
      return '💤 waiting';
    case 'complete':
      return '✅ complete';
    case 'offline':
      return '🔌 offline';
    default:
      return status ? `? ${status}` : '? unknown';
  }
}

export function statusClass(status?: string): string {
  return isTaskStatus(status ?? '') ? status as TaskStatus : 'unknown';
}

export function statusRank(status: TaskStatus): number {
  switch (status) {
    case 'active':
      return 0;
    case 'waiting':
      return 1;
    case 'complete':
      return 2;
    case 'offline':
      return 3;
  }
}

export function agentLabel(agent: string): string {
  switch (agent) {
    case 'claude':
      return 'Claude Code';
    case 'codex':
      return 'Codex';
    case 'gemini':
      return 'Gemini';
    case 'cursor':
      return 'Cursor Agent';
    case 'copilot':
      return 'GitHub Copilot';
    case 'opencode':
      return 'OpenCode';
    default:
      return agent || 'Default agent';
  }
}

export function hasTmuxSession(task?: Task | null): boolean {
  return Boolean(task?.sessionName);
}

export function hasStructuredSession(task?: Task | null): boolean {
  return Boolean(task?.agentThreadId && task?.agentStreamKind);
}

export function isDiscordTask(task?: Task | null): boolean {
  return task?.interface === 'discord';
}

export function taskInterfaceLabel(filter: TaskInterfaceFilter): string {
  switch (filter) {
    case 'desktop':
      return 'Desktop';
    case 'discord':
      return 'Discord';
    default:
      return 'All';
  }
}

export function taskInterfaceCounts(tasks: Task[]): Record<TaskInterfaceFilter, number> {
  const discord = tasks.filter(isDiscordTask).length;
  return {
    all: tasks.length,
    desktop: tasks.length - discord,
    discord,
  };
}

export function tasksForInterfaceFilter(tasks: Task[], filter: TaskInterfaceFilter): Task[] {
  const filtered = tasks.filter((task) => {
    if (filter === 'desktop') return !isDiscordTask(task);
    if (filter === 'discord') return isDiscordTask(task);
    return true;
  });
  return filtered
    .map((task, index) => ({ task, index }))
    .sort((a, b) => {
      const group = Number(isDiscordTask(a.task)) - Number(isDiscordTask(b.task));
      return group !== 0 ? group : a.index - b.index;
    })
    .map(({ task }) => task);
}

export function structuredSessionMessage(task: Task): string {
  return [
    `Discord-attached task "${task.title}".`,
    `Agent: ${agentLabel(task.agent)}`,
    'Continue in the mapped Discord task channel.',
    'Desktop terminal input is disabled for Discord tasks.',
  ].join('\r\n') + '\r\n';
}

export function sortTasks(tasks: Task[]): Task[] {
  return [...tasks].sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

export function taskPreviewDescription(task: Task): string {
  const description = task.description?.trim() ?? '';
  if (!description) return '';
  return quickTaskTemplates.some((template) => template.prompt.trim() === description) ? '' : description;
}
