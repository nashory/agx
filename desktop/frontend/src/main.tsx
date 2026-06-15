import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  CheckCircle2,
  Code2,
  CircleHelp,
  ExternalLink,
  Folder,
  FolderOpen,
  GitBranch,
  Grid2X2,
  Keyboard,
  List,
  LockKeyhole,
  Maximize2,
  MessageCircle,
  Minimize2,
  Minus,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings as SettingsIcon,
  ShieldCheck,
  SquareTerminal,
  Square,
  Sun,
  Trash2,
  X,
} from 'lucide-react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { api, type LogEvent, type MetadataEvent, type MonitorTask } from './api';
import { ActionLogConsole } from './actionLog';
import { CodePreview, isMarkdownPreviewPath, renderMarkdown } from './codePreview';
import { FilePanel } from './filePanel';
import { addUniquePaths, appendPromptPaths, pathsFromDrop } from './pathDrag';
import type { Agent, DiscordStatusInfo, LanguageStat, Project, ProjectCandidate, RuntimeConfigInfo, RuntimeStatusInfo, Task, TaskStatus, TaskTranscriptMessage, ViewMode, WorkspaceMode } from './types';
import './styles.css';

type ThemeMode = 'dark' | 'light';
type MainTab = 'workspace' | 'monitor' | 'discord' | 'shortcuts' | 'settings';
type TaskInterfaceFilter = 'all' | 'desktop' | 'discord';
const mainTabs: MainTab[] = ['workspace', 'monitor', 'discord', 'shortcuts', 'settings'];

type DesktopActionResult = {
  taskID?: string;
  expectSession?: boolean;
};
type DesktopAction = (action: () => Promise<DesktopActionResult | void>, label?: string) => Promise<boolean>;

type UserPreferences = {
  showActionLog: boolean;
  openProjectAfterAdd: boolean;
  defaultTaskView: ViewMode;
  monitorRefreshSeconds: number;
  projectCandidateLimit: number;
  defaultAllMighty: boolean;
};

type QuickTaskTemplate = {
  id: string;
  title: string;
  label: string;
  prompt: string;
};

const quickTaskTemplates: QuickTaskTemplate[] = [
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

const defaultPreferences: UserPreferences = {
  showActionLog: true,
  openProjectAfterAdd: true,
  defaultTaskView: 'grid',
  monitorRefreshSeconds: 5,
  projectCandidateLimit: 18,
  defaultAllMighty: false,
};

const preferenceKey = 'agx-preferences';
const zoomPreferenceKey = 'agx-desktop-zoom';
const defaultZoomLevel = 1;
const minZoomLevel = 0.8;
const maxZoomLevel = 1.5;
const zoomStep = 0.1;

function loadPreferences(): UserPreferences {
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

function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(parsed)) return fallback;
  return Math.min(max, Math.max(min, Math.round(parsed)));
}

function loadZoomLevel(): number {
  const parsed = Number(localStorage.getItem(zoomPreferenceKey));
  return clampZoomLevel(Number.isFinite(parsed) ? parsed : defaultZoomLevel);
}

function clampZoomLevel(value: number): number {
  return Math.min(maxZoomLevel, Math.max(minZoomLevel, Math.round(value * 10) / 10));
}

function terminalTheme(theme: ThemeMode) {
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

function isTextEntry(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  return Boolean(element?.closest('button, input, textarea, select, a, [contenteditable="true"]'));
}

function isTerminalInput(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  return Boolean(element?.closest('.terminal-host, .task-output-terminal, .split-terminal'));
}

function focusSidebarNavigation() {
  const activeButton = document.querySelector<HTMLElement>('.sidebar-button.active');
  if (activeButton) {
    activeButton.focus();
    return;
  }
  document.querySelector<HTMLElement>('.sidebar-nav')?.focus();
}

function focusMainContent() {
  requestAnimationFrame(() => document.querySelector<HTMLElement>('.app-content')?.focus());
}

function projectGridColumns(grid: HTMLElement | null): number {
  if (!grid) return 1;
  const firstCard = grid.querySelector<HTMLElement>('.project-card');
  if (!firstCard) return 1;
  const gap = Number.parseFloat(window.getComputedStyle(grid).columnGap) || 0;
  return Math.max(1, Math.floor((grid.clientWidth + gap) / (firstCard.offsetWidth + gap)));
}

function errorMessage(err: unknown): string {
  return humanizeErrorMessage(err instanceof Error ? err.message : String(err));
}

function humanizeErrorMessage(message: string): string {
  const cleaned = message.replace(/^runtime API [A-Z]+ [^ ]+ failed: \d{3} [^:]+: /, '').trim();
  const activeProjectTask = cleaned.match(/another project-mode task is already active for this project: ([\w-]+)/i);
  if (activeProjectTask) {
    return `Another project-mode task is already active for this project. Stop task ${activeProjectTask[1]} or choose Worktree mode before creating a new task.`;
  }
  return cleaned || message;
}

function isAgentContextClearCommand(message: string): boolean {
  return message.trim().toLowerCase() === '/clear';
}

function timestamp(): string {
  return new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function relativeTime(value: string): string {
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

function isTaskStatus(value: string): value is TaskStatus {
  return value === 'active' || value === 'waiting' || value === 'complete' || value === 'offline';
}

function statusLabel(status?: string): string {
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

function statusClass(status?: string): string {
  return isTaskStatus(status ?? '') ? status as TaskStatus : 'unknown';
}

function statusRank(status: TaskStatus): number {
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

function agentLabel(agent: string): string {
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

function hasTmuxSession(task?: Task | null): boolean {
  return Boolean(task?.sessionName);
}

function hasStructuredSession(task?: Task | null): boolean {
  return Boolean(task?.agentThreadId && task?.agentStreamKind);
}

function isDiscordTask(task?: Task | null): boolean {
  return task?.interface === 'discord';
}

function taskInterfaceLabel(filter: TaskInterfaceFilter): string {
  switch (filter) {
    case 'desktop':
      return 'Desktop';
    case 'discord':
      return 'Discord';
    default:
      return 'All';
  }
}

function taskInterfaceCounts(tasks: Task[]): Record<TaskInterfaceFilter, number> {
  const discord = tasks.filter(isDiscordTask).length;
  return {
    all: tasks.length,
    desktop: tasks.length - discord,
    discord,
  };
}

function tasksForInterfaceFilter(tasks: Task[], filter: TaskInterfaceFilter): Task[] {
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

function structuredSessionMessage(task: Task): string {
  return [
    `Discord-attached task "${task.title}".`,
    `Agent: ${agentLabel(task.agent)}`,
    'Continue in the mapped Discord task channel.',
    'Desktop terminal input is disabled for Discord tasks.',
  ].join('\r\n') + '\r\n';
}

function sortTasks(tasks: Task[]): Task[] {
  return [...tasks].sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

function taskPreviewDescription(task: Task): string {
  const description = task.description?.trim() ?? '';
  if (!description) return '';
  return quickTaskTemplates.some((template) => template.prompt.trim() === description) ? '' : description;
}

function App() {
  const [activeTab, setActiveTab] = useState<MainTab>(() => {
    const stored = localStorage.getItem('agx-active-tab');
    return stored === 'monitor' || stored === 'discord' || stored === 'shortcuts' || stored === 'settings' ? stored : 'workspace';
  });
  const [preferences, setPreferences] = useState<UserPreferences>(() => loadPreferences());
  const [projects, setProjects] = useState<Project[]>([]);
  const [project, setProject] = useState<Project | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [monitorTasks, setMonitorTasks] = useState<MonitorTask[]>([]);
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatusInfo>({ running: false, uptimeSeconds: 0, socketPath: '', lockPath: '', recovery: {} });
  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfigInfo>({ defaultAgent: 'codex' });
  const [globalAgents, setGlobalAgents] = useState<Agent[]>([]);
  const [runtimeChecked, setRuntimeChecked] = useState(false);
  const [discordStatus, setDiscordStatus] = useState<DiscordStatusInfo>({ enabled: false, connected: false, uptimeSeconds: 0, sync: { running: false } });
  const [discordStatusLoading, setDiscordStatusLoading] = useState(true);
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [splitTaskIDs, setSplitTaskIDs] = useState<string[]>([]);
  const [viewMode, setViewMode] = useState<ViewMode>(() => preferences.defaultTaskView);
  const [theme, setTheme] = useState<ThemeMode>(() => (localStorage.getItem('agx-theme') === 'light' ? 'light' : 'dark'));
  const [zoomLevel, setZoomLevel] = useState(() => loadZoomLevel());
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState<{ title: string; message: string } | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const trackedRuntimeTaskIDsKey = useMemo(
    () => tasks.filter((task) => task.sessionName).map((task) => task.id).join('\0'),
    [tasks],
  );

  const appendLog = useCallback((message: string) => {
    setLogs((value) => [...value.slice(-199), `${timestamp()} ${message}`]);
  }, []);

  const applyTaskOrder = useCallback((fetched: Task[]) => {
    const ordered = sortTasks(fetched);
    setTasks(ordered);
    return ordered;
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('agx-theme', theme);
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('agx-active-tab', activeTab);
  }, [activeTab]);

  useEffect(() => {
    localStorage.setItem(preferenceKey, JSON.stringify(preferences));
  }, [preferences]);

  useEffect(() => {
    const rounded = clampZoomLevel(zoomLevel);
    document.documentElement.style.setProperty('--app-zoom', rounded.toFixed(2));
    document.documentElement.style.setProperty('--app-zoom-inverse', (1 / rounded).toFixed(6));
    localStorage.setItem(zoomPreferenceKey, String(rounded));
  }, [zoomLevel]);

  const toggleTheme = useCallback(() => {
    setTheme((value) => (value === 'dark' ? 'light' : 'dark'));
  }, []);

  const switchMainTab = useCallback((tab: MainTab) => {
    setActiveTab(tab);
    setSelectedTask(null);
    setSplitTaskIDs([]);
    if (tab === 'workspace') {
      setProject(null);
      setTasks([]);
      setViewMode(preferences.defaultTaskView);
    }
  }, [preferences.defaultTaskView]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
      if (event.key !== '+' && event.key !== '=' && event.key !== '-' && event.key !== '_' && event.key !== '0') return;
      event.preventDefault();
      event.stopPropagation();
      if (event.key === '0') {
        setZoomLevel(defaultZoomLevel);
        return;
      }
      const direction = event.key === '-' || event.key === '_' ? -1 : 1;
      setZoomLevel((current) => clampZoomLevel(current + direction * zoomStep));
    };

    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (isTerminalInput(event.target)) return;
      event.preventDefault();
      event.stopPropagation();
      focusSidebarNavigation();
    };

    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      if (isTextEntry(event.target)) return;
      if (document.querySelector('.modal-backdrop')) return;

      const index = Number(event.key) - 1;
      const tab = mainTabs[index];
      if (!tab) return;
      event.preventDefault();
      switchMainTab(tab);
    };

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [switchMainTab]);

  const loadProjects = useCallback(async () => {
    setError('');
    try {
      setProjects(await api.ListProjects());
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] list projects: ${message}`);
    }
  }, [appendLog]);

  const loadTasks = useCallback(async (projectID: string) => {
    setError('');
    try {
      const fetched = await api.ListTasks(projectID);
      applyTaskOrder(fetched);
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] list tasks: ${message}`);
    }
  }, [appendLog, applyTaskOrder]);

  const loadMonitorTasks = useCallback(async () => {
    setError('');
    try {
      const [currentProjects, rows] = await Promise.all([
        api.ListProjects(),
        api.ListMonitorTasks(),
      ]);
      setProjects(currentProjects);
      setMonitorTasks(sortTasks(rows) as MonitorTask[]);
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] monitor refresh: ${message}`);
    }
  }, [appendLog]);

  const loadDiscordStatus = useCallback(async () => {
    setDiscordStatusLoading(true);
    try {
      setDiscordStatus(await api.DiscordStatus());
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] discord status: ${message}`);
    } finally {
      setDiscordStatusLoading(false);
    }
  }, [appendLog]);

  const loadRuntimeStatus = useCallback(async () => {
    try {
      setRuntimeStatus(await api.RuntimeStatus());
    } catch (err) {
      const message = errorMessage(err);
      setRuntimeStatus((current) => ({ ...current, running: false, error: message }));
      appendLog(`[error] runtime status: ${message}`);
    } finally {
      setRuntimeChecked(true);
    }
  }, [appendLog]);

  const loadRuntimeConfig = useCallback(async () => {
    try {
      setRuntimeConfig(await api.RuntimeConfig());
    } catch (err) {
      appendLog(`[error] runtime config: ${errorMessage(err)}`);
    }
  }, [appendLog]);

  const loadGlobalAgents = useCallback(async () => {
    try {
      setGlobalAgents(await api.ListAvailableAgents(''));
    } catch (err) {
      appendLog(`[error] list agents: ${errorMessage(err)}`);
      setGlobalAgents([]);
    }
  }, [appendLog]);

  const updateDefaultAgent = useCallback(async (agentName: string) => {
    setBusy(true);
    setError('');
    appendLog(`$ set default agent ${agentLabel(agentName)}`);
    try {
      const cfg = await api.UpdateDefaultAgent(agentName);
      setRuntimeConfig(cfg);
      appendLog(`[ok] default agent ${agentLabel(cfg.defaultAgent)}`);
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] default agent: ${message}`);
      throw err;
    } finally {
      setBusy(false);
    }
  }, [appendLog]);

  const runRuntimeAction = useCallback(async (action: () => Promise<RuntimeStatusInfo>, label: string) => {
    setBusy(true);
    setError('');
    appendLog(`$ ${label}`);
    try {
      const status = await action();
      setRuntimeStatus(status);
      appendLog(`[ok] ${label}`);
      return status;
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] ${label}: ${message}`);
      void loadRuntimeStatus();
      throw err;
    } finally {
      setBusy(false);
    }
  }, [appendLog, loadRuntimeStatus]);

  useEffect(() => {
    void loadRuntimeStatus();
    void loadRuntimeConfig();
    void loadGlobalAgents();
    void loadProjects();
    void loadDiscordStatus();
  }, [loadProjects, loadDiscordStatus, loadRuntimeConfig, loadGlobalAgents, loadRuntimeStatus]);

  useEffect(() => {
    const timer = window.setInterval(() => void loadRuntimeStatus(), 5000);
    return () => window.clearInterval(timer);
  }, [loadRuntimeStatus]);

  useEffect(() => {
    if (activeTab !== 'settings') return;
    void loadRuntimeConfig();
    void loadGlobalAgents();
  }, [activeTab, loadGlobalAgents, loadRuntimeConfig]);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn?.('discord:status', (payload) => {
      setDiscordStatus(payload as DiscordStatusInfo);
      setDiscordStatusLoading(false);
    });
    return () => unsubscribe?.();
  }, []);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn?.('runtime:status', (payload) => {
      setRuntimeStatus(payload as RuntimeStatusInfo);
      setRuntimeChecked(true);
    });
    return () => unsubscribe?.();
  }, []);

  useEffect(() => {
    if (!project) return;
    void loadTasks(project.id);
  }, [project, loadTasks]);

  useEffect(() => {
    if (activeTab !== 'monitor') return;
    void loadMonitorTasks();
    const timer = window.setInterval(() => void loadMonitorTasks(), preferences.monitorRefreshSeconds * 1000);
    return () => window.clearInterval(timer);
  }, [activeTab, loadMonitorTasks, preferences.monitorRefreshSeconds]);

  useEffect(() => {
    if (activeTab !== 'discord') return;
    void loadDiscordStatus();
  }, [activeTab, loadDiscordStatus]);

  useEffect(() => {
    if (!trackedRuntimeTaskIDsKey) return;
    const taskIDs = trackedRuntimeTaskIDsKey.split('\0');
    let cancelled = false;
    const refreshStates = async () => {
      const updates: Array<{ taskID: string; status?: TaskStatus; missing?: boolean } | null> = await Promise.all(taskIDs.map(async (taskID) => {
        try {
          const status = await api.GetTaskStatus(taskID);
          return isTaskStatus(status) ? { taskID, status } : null;
        } catch (err) {
          const message = errorMessage(err);
          if (message.includes('404 Not Found') || message.includes('task not found')) {
            return { taskID, missing: true };
          }
          appendLog(`[error] refresh task ${taskID}: ${message}`);
          return null;
        }
      }));
      if (cancelled) return;
      const missingIDs = new Set(updates.filter((item) => item && 'missing' in item).map((item) => item?.taskID));
      setTasks((current) => current.filter((task) => !missingIDs.has(task.id)).map((task) => {
        const update = updates.find((item) => item?.taskID === task.id);
        if (update?.status && task.status !== update.status) {
          appendLog(`[status] ${task.title}: ${task.status} -> ${update.status}`);
        }
        return update?.status && task.status !== update.status ? { ...task, status: update.status } : task;
      }));
    };
    void refreshStates();
    const timer = window.setInterval(() => void refreshStates(), 1500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [trackedRuntimeTaskIDsKey, appendLog]);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn?.('agx:metadata', (payload) => {
      const event = payload as MetadataEvent;
      void loadProjects();
      if (activeTab === 'monitor') void loadMonitorTasks();
      if (project && (!event.projectId || event.projectId === project.id)) {
        void loadTasks(project.id);
      }
    });
    return () => unsubscribe?.();
  }, [activeTab, project, loadProjects, loadMonitorTasks, loadTasks]);

  useEffect(() => {
    if (!selectedTask) return;
    const fresh = tasks.find((task) => task.id === selectedTask.id);
    if (fresh && fresh !== selectedTask) {
      setSelectedTask(fresh);
      return;
    }
    if (!fresh) {
      setSelectedTask(null);
    }
  }, [selectedTask, tasks]);

  useEffect(() => {
    if (splitTaskIDs.length === 0) return;
    const taskIDs = new Set(tasks.map((task) => task.id));
    setSplitTaskIDs((current) => {
      const next = current.filter((taskID) => taskIDs.has(taskID));
      return next.length === current.length ? current : next;
    });
  }, [splitTaskIDs.length, tasks]);

  async function runAction(action: () => Promise<DesktopActionResult | void>, label = 'Action') {
    setBusy(true);
    setError('');
    setActionError(null);
    appendLog(`$ ${label}`);
    try {
      const result = await action();
      let nextTasks: Task[] | null = null;
      if (project) {
        const fetched = await api.ListTasks(project.id);
        nextTasks = applyTaskOrder(fetched);
      }
      await loadProjects();
      appendLog(`[ok] ${label}`);
      if (result?.expectSession && result.taskID && nextTasks) {
        const task = nextTasks.find((item) => item.id === result.taskID);
        if (!task) {
          appendLog(`[warn] ${label}: task disappeared after action`);
        } else if (!task.sessionName || task.status === 'offline') {
          appendLog(`[warn] ${label}: task has no active session after action (status=${task.status})`);
        }
      }
      return true;
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      setActionError({ title: label, message });
      appendLog(`[error] ${label}: ${message}`);
      if (project) {
        void loadTasks(project.id);
        void loadProjects();
      }
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function resetDatabase() {
    setBusy(true);
    setError('');
    appendLog('$ reset database');
    try {
      await api.ResetDatabase();
      setProjects([]);
      setProject(null);
      setTasks([]);
      setMonitorTasks([]);
      setSelectedTask(null);
      setSplitTaskIDs([]);
      appendLog('[ok] reset database');
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      appendLog(`[error] reset database: ${message}`);
      throw err;
    } finally {
      setBusy(false);
    }
  }

  let content: React.ReactNode;

  const runtimeUnavailable = runtimeChecked && !runtimeStatus.running;

  if (activeTab === 'settings') {
    content = (
      <SettingsView
        preferences={preferences}
        onPreferencesChange={setPreferences}
        theme={theme}
        onThemeChange={setTheme}
        onToggleTheme={toggleTheme}
        onResetDatabase={resetDatabase}
        runtimeStatus={runtimeStatus}
        runtimeConfig={runtimeConfig}
        agents={globalAgents}
        onDefaultAgentChange={updateDefaultAgent}
        onRefreshRuntime={loadRuntimeStatus}
        onStartRuntime={() => runRuntimeAction(api.RuntimeStart, 'start runtime')}
        onInstallRuntimeService={() => runRuntimeAction(api.RuntimeInstallService, 'install runtime service')}
        onStopRuntime={() => runRuntimeAction(api.RuntimeStop, 'stop runtime')}
        busy={busy}
      />
    );
  } else if (activeTab === 'shortcuts') {
    content = <ShortcutsView theme={theme} onToggleTheme={toggleTheme} />;
  } else if (runtimeUnavailable) {
    content = (
      <RuntimeStartupView
        runtimeStatus={runtimeStatus}
        busy={busy}
        onRefreshRuntime={loadRuntimeStatus}
        onStartRuntime={() => runRuntimeAction(api.RuntimeStart, 'start runtime')}
        onInstallRuntimeService={() => runRuntimeAction(api.RuntimeInstallService, 'install runtime service')}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  } else if (activeTab === 'discord') {
    content = (
      <DiscordView
        status={discordStatus}
        statusLoading={discordStatusLoading}
        onStatus={setDiscordStatus}
        onRefresh={loadDiscordStatus}
        onLog={appendLog}
        onError={setError}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  } else if (activeTab === 'monitor') {
    content = (
      <MonitorView
        tasks={monitorTasks}
        projects={projects}
        error={error}
        refreshSeconds={preferences.monitorRefreshSeconds}
        onRefresh={loadMonitorTasks}
        onOpenWorkspace={(projectID, taskID) => {
          const nextProject = projects.find((item) => item.id === projectID);
          if (!nextProject) return;
          setViewMode(preferences.defaultTaskView);
          setProject(nextProject);
          setActiveTab('workspace');
          void api.ListTasks(projectID).then((projectTasks) => {
            setTasks(projectTasks);
            const nextTask = projectTasks.find((item) => item.id === taskID);
            if (nextTask) setSelectedTask(nextTask);
          });
        }}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  } else if (selectedTask && project) {
    content = (
      <SessionView
        project={project}
        task={selectedTask}
        onBack={() => {
          setSelectedTask(null);
          void loadTasks(project.id);
        }}
        onError={setError}
        onLog={appendLog}
        onChanged={() => loadTasks(project.id)}
        error={error}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  } else if (project && splitTaskIDs.length > 0) {
    const splitTasks = splitTaskIDs
      .map((id) => tasks.find((task) => task.id === id))
      .filter((task): task is Task => Boolean(task));
    if (splitTasks.length > 0) {
      content = (
        <SplitView
          project={project}
          tasks={splitTasks}
          onBack={() => setSplitTaskIDs([])}
          onRemove={(id) => setSplitTaskIDs((value) => value.filter((taskID) => taskID !== id))}
          onError={setError}
          error={error}
          theme={theme}
          onToggleTheme={toggleTheme}
        />
      );
    } else {
      content = null;
    }
  } else if (project) {
    content = (
      <TaskView
        project={project}
        tasks={tasks}
        viewMode={viewMode}
        defaultAllMighty={preferences.defaultAllMighty}
        discordConnected={discordStatus.connected}
        busy={busy}
        error={error}
        onBack={() => {
          setProject(null);
          setTasks([]);
          void loadProjects();
        }}
        onRefresh={() => loadTasks(project.id)}
        onViewMode={setViewMode}
        onSelectTask={setSelectedTask}
        onSplitTask={(task) => setSplitTaskIDs((value) => Array.from(new Set([...value, task.id])).slice(-4))}
        onAction={runAction}
        onLog={appendLog}
        onProjectChanged={(nextProject) => {
          setProject(nextProject);
          setProjects((current) => current.map((item) => (item.id === nextProject.id ? nextProject : item)));
        }}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  } else {
    content = (
      <ProjectView
        projects={projects}
        error={error}
        candidateLimit={preferences.projectCandidateLimit}
        openProjectAfterAdd={preferences.openProjectAfterAdd}
        onRefresh={loadProjects}
        onOpenProject={(nextProject) => {
          setViewMode(preferences.defaultTaskView);
          setProject(nextProject);
        }}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    );
  }

  return (
    <>
      <AppFrame
        activeTab={activeTab}
        project={project}
        liveCount={monitorTasks.filter((task) => task.status === 'active' || task.status === 'waiting').length}
        discordStatus={discordStatus}
        onTabChange={switchMainTab}
      >
        {content}
      </AppFrame>
      {actionError && <ActionErrorDialog title={actionError.title} message={actionError.message} onClose={() => setActionError(null)} />}
      {preferences.showActionLog && <ActionLogConsole logs={logs} />}
    </>
  );
}

function AppFrame({
  activeTab,
  project,
  liveCount,
  discordStatus,
  onTabChange,
  children,
}: {
  activeTab: MainTab;
  project: Project | null;
  liveCount: number;
  discordStatus: DiscordStatusInfo;
  onTabChange: (tab: MainTab) => void;
  children: React.ReactNode;
}) {
  function handleSidebarKeyDown(event: React.KeyboardEvent<HTMLElement>) {
    const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('.sidebar-button'));
    if (buttons.length === 0) return;
    const activeElement = document.activeElement as HTMLButtonElement | null;
    const currentIndex = Math.max(0, buttons.indexOf(activeElement ?? buttons[0]));
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const direction = event.key === 'ArrowDown' ? 1 : -1;
      const nextIndex = (currentIndex + direction + buttons.length) % buttons.length;
      buttons[nextIndex].focus();
      return;
    }
    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      buttons[event.key === 'Home' ? 0 : buttons.length - 1].focus();
      return;
    }
    const tabIndex = Number(event.key) - 1;
    if (Number.isInteger(tabIndex) && tabIndex >= 0 && tabIndex < buttons.length) {
      event.preventDefault();
      buttons[tabIndex].click();
      buttons[tabIndex].focus();
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      buttons[currentIndex].click();
      focusMainContent();
    }
  }

  return (
    <div className="desktop-frame">
      <aside className="app-sidebar">
        <div className="sidebar-brand">
          <strong>AGX</strong>
          <span>{project ? project.name : 'Agent workspace'}</span>
        </div>
        <nav className="sidebar-nav" aria-label="Primary" tabIndex={-1} onKeyDown={handleSidebarKeyDown}>
          <SidebarButton
            active={activeTab === 'workspace'}
            label="Workspace"
            detail={project ? 'Project tasks' : 'Projects'}
            onClick={() => onTabChange('workspace')}
          >
            <Folder size={18} />
          </SidebarButton>
          <SidebarButton
            active={activeTab === 'monitor'}
            label="Monitor"
            detail={liveCount > 0 ? `${liveCount} live` : 'No live agents'}
            onClick={() => onTabChange('monitor')}
          >
            <Activity size={18} />
          </SidebarButton>
          <SidebarButton
            active={activeTab === 'discord'}
            label="Discord"
            detail={discordSidebarDetail(discordStatus)}
            indicator={discordSidebarIndicator(discordStatus)}
            onClick={() => onTabChange('discord')}
          >
            <MessageCircle size={18} />
          </SidebarButton>
          <SidebarButton
            active={activeTab === 'shortcuts'}
            label="Shortcuts"
            detail="Keyboard"
            onClick={() => onTabChange('shortcuts')}
          >
            <Keyboard size={18} />
          </SidebarButton>
          <SidebarButton
            active={activeTab === 'settings'}
            label="Settings"
            detail="Preferences"
            onClick={() => onTabChange('settings')}
          >
            <SettingsIcon size={18} />
          </SidebarButton>
        </nav>
      </aside>
      <section className="app-content" tabIndex={-1}>{children}</section>
    </div>
  );
}

function discordSidebarDetail(status: DiscordStatusInfo): string {
  if (status.connected) return 'Connected';
  if (status.enabled) return 'Disconnected';
  return 'Not configured';
}

function discordSidebarIndicator(status: DiscordStatusInfo): SidebarIndicator {
  if (status.connected) return 'ok';
  if (status.enabled || status.error) return 'error';
  return 'neutral';
}

type SidebarIndicator = 'neutral' | 'ok' | 'error';

function SidebarButton({
  active,
  label,
  detail,
  indicator,
  onClick,
  children,
}: {
  active: boolean;
  label: string;
  detail: string;
  indicator?: SidebarIndicator;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button className={`sidebar-button ${active ? 'active' : ''}`} onClick={onClick}>
      <span className="sidebar-icon">{children}</span>
      <span>
        <strong>
          {label}
          {indicator && <i className={`sidebar-indicator ${indicator}`} aria-hidden="true" />}
        </strong>
        <small>{detail}</small>
      </span>
    </button>
  );
}

function MonitorView({
  tasks,
  projects,
  error,
  refreshSeconds,
  onRefresh,
  onOpenWorkspace,
  theme,
  onToggleTheme,
}: {
  tasks: MonitorTask[];
  projects: Project[];
  error: string;
  refreshSeconds: number;
  onRefresh: () => void;
  onOpenWorkspace: (projectID: string, taskID: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const activeCount = tasks.filter((task) => task.status === 'active').length;
  const waitingCount = tasks.filter((task) => task.status === 'waiting').length;
  const completeCount = tasks.filter((task) => task.status === 'complete').length;
  const offlineCount = tasks.filter((task) => task.status === 'offline').length;
  const projectCount = new Set(tasks.map((task) => task.projectId)).size;

  return (
    <main className="app-shell monitor-view">
      <Header title="Monitor" subtitle="Agent task status across registered workspaces" theme={theme} onToggleTheme={onToggleTheme}>
        <IconButton label="Refresh monitor" onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      <section className="monitor-summary">
        <MetricCard label="Tasks" value={tasks.length} detail={`Auto-refreshes every ${refreshSeconds}s`} />
        <MetricCard label="Active" value={activeCount} detail="Output is changing" />
        <MetricCard label="Waiting" value={waitingCount} detail="Session is idle" />
        <MetricCard label="Complete" value={completeCount} detail="Agent returned to shell" />
        <MetricCard label="Offline" value={offlineCount} detail={`${projectCount}/${projects.length} projects shown`} />
      </section>
      {tasks.length === 0 ? (
        <EmptyState title="No tasks" detail="Create a task from Workspace to start monitoring agent state." />
      ) : (
        <section className="monitor-table">
          <div className="monitor-row monitor-head">
            <span>Task</span>
            <span>Project</span>
            <span>Status</span>
            <span>Agent</span>
            <span>Mode</span>
            <span>Updated</span>
            <span />
          </div>
          {tasks.map((task) => (
            <div className="monitor-row" key={task.id}>
              <span>
                <strong>{task.title}</strong>
                {taskPreviewDescription(task) && <small>{taskPreviewDescription(task)}</small>}
              </span>
              <span>
                <strong>{task.projectName}</strong>
                <small>{task.projectPath}</small>
              </span>
              <span className="monitor-status-cell">
                <span className={`status-pill ${statusClass(task.status)}`} title={`Status: ${task.status || 'unknown'}`}>{statusLabel(task.status)}</span>
                {task.sessionName && <small>{task.sessionName}</small>}
              </span>
              <span><AgentBadge agent={task.agent} /></span>
              <span>{task.allMighty ? <AllMightyBadge /> : 'Standard'}</span>
              <span>{new Date(task.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
              <button className="text-button" onClick={() => onOpenWorkspace(task.projectId, task.id)}>Open</button>
            </div>
          ))}
        </section>
      )}
    </main>
  );
}

function DiscordView({
  status,
  statusLoading,
  onStatus,
  onRefresh,
  onLog,
  onError,
  theme,
  onToggleTheme,
}: {
  status: DiscordStatusInfo;
  statusLoading: boolean;
  onStatus: (status: DiscordStatusInfo) => void;
  onRefresh: () => Promise<void>;
  onLog: (message: string) => void;
  onError: (message: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [token, setToken] = useState('');
  const [guildID, setGuildID] = useState(status.guildId ?? '');
  const [allowedUserID, setAllowedUserID] = useState('');
  const [events, setEvents] = useState<string[]>([]);
  const [busyAction, setBusyAction] = useState<'connect' | 'disconnect' | 'sync' | 'reset' | 'invite' | null>(null);
  const [confirmingReset, setConfirmingReset] = useState(false);
  const busy = busyAction !== null;
  const discordRunningElsewhere = status.error?.includes('discord bridge already running') ?? false;
  const checkingConnection = statusLoading || (status.enabled && !status.connected && !status.error && !discordRunningElsewhere);
  const hasStoredToken = Boolean(status.maskedBotToken);
  const shouldShowDisconnect = status.connected || discordRunningElsewhere;
  const hardSyncRunning = status.sync?.running && status.sync.kind === 'hard';
  const statusLabel = checkingConnection ? 'checking' : status.connected ? 'connected' : discordRunningElsewhere ? 'already running' : status.enabled ? 'disconnected' : 'disabled';
  const statusTone = status.connected ? 'active' : checkingConnection || status.enabled || discordRunningElsewhere ? 'waiting' : 'offline';
  const statusDetail = checkingConnection
    ? 'Checking Discord connection...'
    : status.error || (status.connected ? `Connected${status.guildName ? ` to ${status.guildName}` : ''}` : status.enabled ? 'Enabled but disconnected' : 'Not configured');
  const canReuseStoredToken = hasStoredToken && status.enabled;
  const missingRequiredToken = token.trim() === '' && !canReuseStoredToken;
  const tokenLocked = hasStoredToken && (checkingConnection || shouldShowDisconnect) && token.trim() === '';
  const connectionLocked = busy || checkingConnection;
  const syncLocked = connectionLocked || hardSyncRunning;

  useEffect(() => {
    setGuildID(status.guildId ?? '');
    setAllowedUserID((status.allowedUserIds ?? [])[0] ?? '');
  }, [status.guildId, status.allowedUserIds]);

  useEffect(() => {
    const detail = checkingConnection ? 'Checking connection' : status.error || (status.connected ? 'Connected' : status.enabled ? 'Disconnected' : 'Disabled');
    setEvents((value) => [`${timestamp()} ${detail}`, ...value].slice(0, 20));
  }, [checkingConnection, status.connected, status.enabled, status.error]);

  useEffect(() => {
    if (!status.sync?.stage) return;
    const detail = status.sync.error ? `${status.sync.stage}: ${status.sync.error}` : status.sync.stage;
    setEvents((value) => [`${timestamp()} ${detail}`, ...value].slice(0, 20));
  }, [status.sync?.stage, status.sync?.error]);

  async function connect() {
    if (missingRequiredToken) {
      onError('Discord bot token is required');
      setEvents((value) => [`${timestamp()} Bot token is required`, ...value].slice(0, 20));
      return;
    }
    if (!guildID.trim()) {
      onError('Discord server ID is required');
      return;
    }
    if (!allowedUserID.trim()) {
      onError('Allowed Discord user ID is required');
      return;
    }
    setBusyAction('connect');
    onError('');
    onLog('$ discord connect');
    try {
      const next = await api.DiscordConnect(token, guildID, allowedUserID);
      onStatus(next);
      setToken('');
      setGuildID(next.guildId ?? '');
      setAllowedUserID((next.allowedUserIds ?? [])[0] ?? '');
      setEvents((value) => [`${timestamp()} Connect requested`, ...value].slice(0, 20));
      onLog('[ok] discord connect');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord connect: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function disconnect() {
    setBusyAction('disconnect');
    onError('');
    onLog('$ discord disconnect');
    try {
      onStatus(await api.DiscordDisconnect());
      setEvents((value) => [`${timestamp()} Disconnect requested`, ...value].slice(0, 20));
      onLog('[ok] discord disconnect');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord disconnect: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function syncNow() {
    setBusyAction('sync');
    onError('');
    onLog('$ discord soft sync');
    try {
      const next = await api.DiscordSoftSync();
      onStatus(next);
      setEvents((value) => [`${timestamp()} Soft sync completed`, ...value].slice(0, 20));
      onLog('[ok] discord soft sync');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord soft sync: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function inviteBot() {
    setBusyAction('invite');
    onError('');
    onLog('$ discord invite bot');
    try {
      await api.OpenDiscordInvite(token);
      setEvents((value) => [`${timestamp()} Invite opened in browser`, ...value].slice(0, 20));
      onLog('[ok] discord invite bot');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord invite bot: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  function requestDiscordReset() {
    onLog('$ discord hard sync prompt');
    setConfirmingReset(true);
  }

  async function resetDiscordServer() {
    setConfirmingReset(false);
    setBusyAction('reset');
    onError('');
    onLog('$ discord hard sync');
    try {
      const next = await api.DiscordHardSync();
      onStatus(next);
      setEvents((value) => [`${timestamp()} ${next.sync?.stage ?? 'Hard sync started'}`, ...value].slice(0, 20));
      onLog(next.sync?.running ? '[ok] discord hard sync started' : '[ok] discord hard sync');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord hard sync: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  return (
    <main className="app-shell">
      <Header title="Discord" subtitle="Remote AGX control from Discord" theme={theme} onToggleTheme={onToggleTheme}>
        <IconButton label="Refresh Discord status" disabled={statusLoading} onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <section className="settings-grid discord-grid">
        <section className="settings-panel discord-connection-panel">
          <h2>Connection</h2>
          {status.error && (
            <div className="discord-error">
              <strong>{discordRunningElsewhere ? 'Connection already active' : 'Connection failed'}</strong>
              <span>{status.error}</span>
            </div>
          )}
          <div className="setting-row">
            <div>
              <strong>Status</strong>
              <span>{statusDetail}</span>
            </div>
            <span className={`status-pill ${statusTone}`}>
              {checkingConnection && <span className="button-spinner" aria-hidden="true" />}
              {statusLabel}
            </span>
          </div>
          <div className="setting-row">
            <div>
              <strong>Server ID</strong>
              <span>{status.guildName ? `Connected server: ${status.guildName}` : 'Saved locally after you connect; kept after disconnect for convenience.'}</span>
            </div>
            <input value={guildID} disabled={connectionLocked} onChange={(event) => setGuildID(event.target.value)} placeholder="1234567890123456789" />
          </div>
          <div className="setting-row">
            <div>
              <strong>Bot token</strong>
              <span>{canReuseStoredToken ? 'Stored token is locked.' : 'Required to connect; disconnect clears the stored token.'}</span>
            </div>
            {tokenLocked ? (
              <div className="locked-token-field" title="Stored bot token">
                <LockKeyhole size={15} aria-hidden="true" />
                <span>{status.maskedBotToken}</span>
              </div>
            ) : (
              <input value={token} type="password" disabled={connectionLocked} onChange={(event) => setToken(event.target.value)} placeholder={canReuseStoredToken ? 'Leave blank to retry with stored token' : 'Discord bot token'} />
            )}
          </div>
          <div className="setting-row">
            <div>
              <strong>Allowed user ID</strong>
              <span>Single Discord user allowed to control AGX; saved locally after connect.</span>
            </div>
            <input value={allowedUserID} disabled={connectionLocked} onChange={(event) => setAllowedUserID(event.target.value)} placeholder="123456789012345678" />
          </div>
          <div className="setting-actions">
            <button className="text-button" disabled={connectionLocked || missingRequiredToken} onClick={inviteBot} aria-busy={busyAction === 'invite'}>
              {busyAction === 'invite' ? <span className="button-spinner" aria-hidden="true" /> : <ExternalLink size={16} aria-hidden="true" />}
              {busyAction === 'invite' ? 'Opening...' : 'Invite AGX Coding'}
            </button>
            {shouldShowDisconnect ? (
              <button className="text-button" disabled={connectionLocked} onClick={disconnect}>
                {busyAction === 'disconnect' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'disconnect' ? 'Disconnecting...' : 'Disconnect'}
              </button>
            ) : (
              <button className="primary-button" disabled={connectionLocked || missingRequiredToken || !guildID.trim() || !allowedUserID.trim()} onClick={connect} aria-busy={busyAction === 'connect'}>
                {busyAction === 'connect' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'connect' ? 'Connecting...' : 'Connect'}
              </button>
            )}
          </div>
        </section>
        <section className="settings-panel">
          <div className="panel-heading">
            <h2>Sync Status</h2>
            <div className="panel-actions">
              <button className="text-button" disabled={syncLocked || !status.connected} onClick={syncNow}>
                {busyAction === 'sync' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'sync' ? 'Soft syncing...' : 'Soft Sync'}
              </button>
              <button className="danger-button" disabled={connectionLocked || hardSyncRunning || !status.connected} onClick={requestDiscordReset}>
                {(busyAction === 'reset' || hardSyncRunning) && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'reset' ? 'Starting...' : hardSyncRunning ? 'Hard syncing...' : 'Hard Sync'}
              </button>
            </div>
          </div>
          <div className="discord-sync-list">
            {status.sync?.stage && (
              <div>
                <strong>Hard Sync</strong>
                <span>{status.sync.stage}{status.sync.error ? `: ${status.sync.error}` : ''}</span>
              </div>
            )}
            <div>
              <strong>#agx-control</strong>
              <span>{checkingConnection ? 'Checking connection' : status.connected ? 'Created or verified during sync' : 'Connect to verify'}</span>
            </div>
            <div>
              <strong>Projects</strong>
              <span>{checkingConnection ? 'Waiting for Discord' : status.connected ? 'Categories mirror registered projects' : 'Waiting for connection'}</span>
            </div>
            <div>
              <strong>Active tasks</strong>
              <span>{checkingConnection ? 'Waiting for Discord' : status.connected ? 'Channels mirror active and waiting tasks' : 'Waiting for connection'}</span>
            </div>
          </div>
        </section>
        <section className="settings-panel">
          <h2>Event Log</h2>
          <div className="discord-event-log">
            {events.length === 0 ? (
              <span>No Discord events yet.</span>
            ) : events.map((event, index) => (
              <span key={`${event}-${index}`}>{event}</span>
            ))}
          </div>
        </section>
      </section>
      {confirmingReset && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingReset(false)}>
          <section className="confirm-modal danger-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Hard Sync Discord</h2>
            <p>This deletes AGX-managed Discord channels, categories, and message history, clears AGX Discord mappings, and recreates the Discord server view from the current AGX projects and tasks.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingReset(false)}>Cancel</button>
              <button className="danger-button" disabled={connectionLocked || hardSyncRunning} onClick={resetDiscordServer}>Hard Sync</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </main>
  );
}

function splitCSV(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

function MetricCard({ label, value, detail }: { label: string; value: number; detail: string }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function AllMightyBadge() {
  return (
    <span className="all-mighty-badge" title="Runs without approval prompts or sandbox restrictions where supported">
      <ShieldCheck size={13} />
      All-mighty
    </span>
  );
}

function AgentBadge({ agent }: { agent: string }) {
  return (
    <span className={`agent-badge agent-${agent.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`} title={agent || 'Default agent'}>
      {agentLabel(agent)}
    </span>
  );
}

function DiscordBadge() {
  return (
    <span className="discord-task-badge" title="Controlled from Discord">
      <MessageCircle size={13} />
      Discord
    </span>
  );
}

function WorkspaceBadge({ mode }: { mode?: WorkspaceMode }) {
  const projectMode = mode === 'project';
  return (
    <span className={`workspace-task-badge ${projectMode ? 'project' : 'worktree'}`} title={projectMode ? 'Runs in the current project checkout' : 'Runs in an isolated task worktree'}>
      {projectMode ? <FolderOpen size={13} /> : <GitBranch size={13} />}
      {projectMode ? 'Project' : 'Worktree'}
    </span>
  );
}

function transcriptRoleLabel(role: TaskTranscriptMessage['role']): string {
  switch (role) {
    case 'user':
      return 'User';
    case 'assistant':
      return 'AGX';
    case 'tool_trace':
      return 'Trace';
    case 'system':
      return 'System';
    case 'status':
      return 'Status';
    default:
      return role;
  }
}

function transcriptMessagesSignature(messages: TaskTranscriptMessage[]): string {
  return messages
    .map((message) => `${message.id}:${message.role}:${message.body.length}:${message.body.slice(-32)}`)
    .join('|');
}

function TranscriptBody({ body }: { body: string }) {
  const html = useMemo(() => renderMarkdown(body, { preserveLineBreaks: true }), [body]);
  return <div className="discord-message-body markdown-preview" dangerouslySetInnerHTML={{ __html: html || ' ' }} />;
}

type ShortcutGroup = {
  title: string;
  rows: Array<{
    action: string;
    keys: string[];
  }>;
};

const shortcutGroups: ShortcutGroup[] = [
  {
    title: 'Workspace',
    rows: [
      { action: 'Back to projects', keys: ['Alt', 'Backspace'] },
      { action: 'Move between projects or tasks', keys: ['Arrow keys'] },
      { action: 'Open selected project or task', keys: ['Enter', 'or', 'Alt', 'Enter'] },
      { action: 'Focus new task title', keys: ['Ctrl / Cmd', 'N'] },
      { action: 'Switch to grid view', keys: ['Ctrl / Cmd', '1'] },
      { action: 'Switch to list view', keys: ['Ctrl / Cmd', '2'] },
      { action: 'Toggle task output panel', keys: ['Alt', 'T'] },
      { action: 'Reorder tasks', keys: ['Drag'] },
    ],
  },
  {
    title: 'Session',
    rows: [
      { action: 'Back to task cards', keys: ['Alt', 'Backspace'] },
      { action: 'Toggle file tree', keys: ['Ctrl / Cmd', 'B'] },
      { action: 'Toggle Session and Preview panes', keys: ['Ctrl / Cmd', 'Tab'] },
      { action: 'Send message from prompt', keys: ['Ctrl / Cmd', 'Enter'] },
      { action: 'Move focus from prompt to sidebar', keys: ['Escape'] },
    ],
  },
  {
    title: 'App',
    rows: [
      { action: 'Switch sidebar tab', keys: ['1', '2', '3', '4', '5'] },
      { action: 'Zoom in or out', keys: ['Ctrl / Cmd', '+', 'or', '-'] },
      { action: 'Reset zoom', keys: ['Ctrl / Cmd', '0'] },
      { action: 'Toggle fullscreen', keys: ['F11', 'or', 'Ctrl / Cmd', 'F'] },
      { action: 'Close modal or cancel edit', keys: ['Escape'] },
    ],
  },
];

function ShortcutsView({ theme, onToggleTheme }: { theme: ThemeMode; onToggleTheme: () => void }) {
  return (
    <main className="app-shell">
      <Header title="Shortcuts" subtitle="Keyboard reference for AGX desktop" theme={theme} onToggleTheme={onToggleTheme} />
      <section className="shortcuts-grid">
        {shortcutGroups.map((group) => (
          <section className="shortcuts-panel" key={group.title}>
            <h2>{group.title}</h2>
            <div className="shortcut-list">
              {group.rows.map((row) => (
                <ShortcutRow key={`${group.title}-${row.action}-${row.keys.join('-')}`} action={row.action} keys={row.keys} />
              ))}
            </div>
          </section>
        ))}
      </section>
    </main>
  );
}

function ShortcutRow({ action, keys }: { action: string; keys: string[] }) {
  return (
    <div className="shortcut-row">
      <span>{action}</span>
      <span className="shortcut-keys">
        {keys.map((key) => (
          key === 'or' ? <span className="shortcut-or" key={key}>or</span> : <kbd key={key}>{key}</kbd>
        ))}
      </span>
    </div>
  );
}

function runtimeDetail(status: RuntimeStatusInfo): string {
  const parts = [`pid ${status.pid ?? 'unknown'}`];
  if (status.version) parts.push(status.version);
  if (status.uptimeSeconds > 0) parts.push(`up ${formatRuntimeUptime(status.uptimeSeconds)}`);
  return parts.join(' · ');
}

function runtimeRecoveryDetail(status: RuntimeStatusInfo): string {
  const recovery = status.recovery ?? {};
  const offline = recovery.offline ?? 0;
  const cleared = recovery.cleared ?? 0;
  const orphans = recovery.orphans ?? 0;
  return `${offline} offline, ${cleared} stale sessions cleared, ${orphans} orphan worktrees removed`;
}

function formatRuntimeUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return remainingMinutes > 0 ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
}

function HelpBadge({ text }: { text: string }) {
  return (
    <span className="help-badge" data-tooltip={text} tabIndex={0} aria-label={text}>
      <CircleHelp size={13} />
    </span>
  );
}

function SettingHeading({ label, help }: { label: string; help: string }) {
  return (
    <span className="setting-heading">
      <strong>{label}</strong>
      <HelpBadge text={help} />
    </span>
  );
}

function RuntimeStartupView({
  runtimeStatus,
  busy,
  onRefreshRuntime,
  onStartRuntime,
  onInstallRuntimeService,
  theme,
  onToggleTheme,
}: {
  runtimeStatus: RuntimeStatusInfo;
  busy: boolean;
  onRefreshRuntime: () => Promise<void>;
  onStartRuntime: () => Promise<RuntimeStatusInfo>;
  onInstallRuntimeService: () => Promise<RuntimeStatusInfo>;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  return (
    <main className="app-shell">
      <Header title="Runtime" subtitle="AGX daemon is required for projects, tasks, and Discord" theme={theme} onToggleTheme={onToggleTheme} />
      <section className="settings-grid runtime-startup-grid">
        <section className="settings-panel runtime-startup-panel">
          <h2>Daemon</h2>
          <div className="setting-row">
            <div>
              <SettingHeading label="Status" help="Whether the AGX runtime daemon is reachable from Desktop." />
              <span>{runtimeStatus.error || 'Not running'}</span>
            </div>
            <span className="status-pill error" title="Desktop cannot reach the AGX runtime daemon.">
              Stopped
            </span>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Controls" help="Runtime actions. Start runs the daemon for this session; Install registers the macOS user service." />
              <span>Start the runtime for this session or install the macOS user service.</span>
            </div>
            <div className="runtime-control-buttons">
              <button className="text-button" disabled={busy} onClick={() => void onRefreshRuntime()}>
                <RefreshCw size={15} />
                Retry
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onStartRuntime()}>
                <Play size={15} />
                Start
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onInstallRuntimeService()}>
                <ShieldCheck size={15} />
                Install
              </button>
            </div>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Socket" help="Local Unix socket path used by Desktop, CLI, and integrations to talk to the runtime daemon." />
              <span>{runtimeStatus.socketPath || 'Unavailable'}</span>
            </div>
            <SquareTerminal size={18} aria-hidden="true" />
          </div>
        </section>
      </section>
    </main>
  );
}

function SettingsView({
  preferences,
  onPreferencesChange,
  theme,
  onThemeChange,
  onToggleTheme,
  onResetDatabase,
  runtimeStatus,
  runtimeConfig,
  agents,
  onDefaultAgentChange,
  onRefreshRuntime,
  onStartRuntime,
  onInstallRuntimeService,
  onStopRuntime,
  busy,
}: {
  preferences: UserPreferences;
  onPreferencesChange: (preferences: UserPreferences) => void;
  theme: ThemeMode;
  onThemeChange: (theme: ThemeMode) => void;
  onToggleTheme: () => void;
  onResetDatabase: () => Promise<void>;
  runtimeStatus: RuntimeStatusInfo;
  runtimeConfig: RuntimeConfigInfo;
  agents: Agent[];
  onDefaultAgentChange: (agentName: string) => Promise<void>;
  onRefreshRuntime: () => Promise<void>;
  onStartRuntime: () => Promise<RuntimeStatusInfo>;
  onInstallRuntimeService: () => Promise<RuntimeStatusInfo>;
  onStopRuntime: () => Promise<RuntimeStatusInfo>;
  busy: boolean;
}) {
  const [confirmingReset, setConfirmingReset] = useState(false);
  const [savingDefaultAgent, setSavingDefaultAgent] = useState(false);
  const defaultAgentName = runtimeConfig.defaultAgent || 'codex';
  const defaultAgentOptions = agents.some((agent) => agent.name === defaultAgentName)
    ? agents
    : [{ name: defaultAgentName, command: defaultAgentName, description: '', available: false }, ...agents];

  function update<K extends keyof UserPreferences>(key: K, value: UserPreferences[K]) {
    onPreferencesChange({ ...preferences, [key]: value });
  }

  async function saveDefaultAgent(agentName: string) {
    setSavingDefaultAgent(true);
    try {
      await onDefaultAgentChange(agentName);
    } finally {
      setSavingDefaultAgent(false);
    }
  }

  async function resetDatabase() {
    await onResetDatabase();
    setConfirmingReset(false);
  }

  return (
    <main className="app-shell">
      <Header title="Settings" subtitle="Desktop preferences for project onboarding and monitoring" theme={theme} onToggleTheme={onToggleTheme} />
      <section className="settings-grid">
        <section className="settings-panel runtime-settings-panel">
          <h2>Runtime</h2>
          <div className="setting-row">
            <div>
              <SettingHeading label="Daemon" help="The background AGX runtime process that owns sessions, Discord bridge state, sockets, and recovery." />
              <span>{runtimeStatus.running ? runtimeDetail(runtimeStatus) : runtimeStatus.error || 'Not running'}</span>
            </div>
            <button className="text-button" onClick={() => void onRefreshRuntime()}>
              <RefreshCw size={15} />
              Refresh
            </button>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Controls" help="Start launches the daemon now, Install registers the macOS user service, and Stop terminates the current daemon." />
              <span>Start the runtime now, install the macOS user service, or stop the running daemon.</span>
            </div>
            <div className="runtime-control-buttons">
              <button className="text-button" disabled={busy || runtimeStatus.running} onClick={() => void onStartRuntime()}>
                <Play size={15} />
                Start
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onInstallRuntimeService()}>
                <ShieldCheck size={15} />
                Install
              </button>
              <button className="text-button" disabled={busy || !runtimeStatus.running} onClick={() => void onStopRuntime()}>
                <Square size={15} />
                Stop
              </button>
            </div>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Socket" help="Local Unix socket path used by Desktop and the CLI to send requests to the runtime daemon." />
              <span>{runtimeStatus.socketPath || 'Unavailable'}</span>
            </div>
            <span className={`status-pill ${runtimeStatus.running ? 'ok' : 'error'}`} title={runtimeStatus.running ? 'Runtime socket is reachable.' : 'Runtime socket is not reachable.'}>
              {runtimeStatus.running ? 'Running' : 'Stopped'}
            </span>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Recovery" help="Startup cleanup summary for stale task sessions and orphan runtime-owned worktrees." />
              <span>{runtimeRecoveryDetail(runtimeStatus)}</span>
            </div>
            <span className="status-pill neutral" title="Recovery only removes sessions and worktrees that AGX runtime owns.">
              Runtime-owned
            </span>
          </div>
        </section>
        <section className="settings-panel">
          <h2>Appearance</h2>
          <div className="setting-row">
            <div>
              <strong>Theme</strong>
              <span>Controls the desktop color scheme.</span>
            </div>
            <select value={theme} onChange={(event) => onThemeChange(event.target.value === 'light' ? 'light' : 'dark')}>
              <option value="dark">Dark</option>
              <option value="light">Light</option>
            </select>
          </div>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Action log</strong>
              <span>Show the compact command/activity log at the bottom edge.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.showActionLog}
              onChange={(event) => update('showActionLog', event.target.checked)}
            />
          </label>
        </section>
        <section className="settings-panel">
          <h2>Workspace</h2>
          <div className="setting-row">
            <div>
              <strong>Default agent</strong>
              <span>Used when a task or project does not choose a specific agent.</span>
            </div>
            <select value={defaultAgentName} disabled={busy || savingDefaultAgent} onChange={(event) => void saveDefaultAgent(event.target.value)}>
              {defaultAgentOptions.map((agent) => (
                <option key={agent.name} value={agent.name}>
                  {agentLabel(agent.name)}{agent.available ? '' : ' (not installed)'}
                </option>
              ))}
            </select>
          </div>
          <div className="setting-row">
            <div>
              <strong>Default task view</strong>
              <span>Choose the task layout used for newly opened projects.</span>
            </div>
            <select value={preferences.defaultTaskView} onChange={(event) => update('defaultTaskView', event.target.value === 'list' ? 'list' : 'grid')}>
              <option value="grid">Grid</option>
              <option value="list">List</option>
            </select>
          </div>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Open after adding project</strong>
              <span>Jump into a project immediately after it is registered.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.openProjectAfterAdd}
              onChange={(event) => update('openProjectAfterAdd', event.target.checked)}
            />
          </label>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Default all-mighty mode</strong>
              <span>Preselect unrestricted agent permissions when creating a task.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.defaultAllMighty}
              onChange={(event) => update('defaultAllMighty', event.target.checked)}
            />
          </label>
          <div className="setting-row">
            <div>
              <strong>Repository suggestions</strong>
              <span>Maximum number of Git repositories shown in the add-project modal.</span>
            </div>
            <input
              type="number"
              min={6}
              max={50}
              value={preferences.projectCandidateLimit}
              onChange={(event) => update('projectCandidateLimit', clampNumber(event.target.value, 6, 50, defaultPreferences.projectCandidateLimit))}
            />
          </div>
        </section>
        <section className="settings-panel">
          <h2>Monitor</h2>
          <div className="setting-row">
            <div>
              <strong>Refresh interval</strong>
              <span>How often Monitor refreshes live agent status.</span>
            </div>
            <input
              type="number"
              min={2}
              max={60}
              value={preferences.monitorRefreshSeconds}
              onChange={(event) => update('monitorRefreshSeconds', clampNumber(event.target.value, 2, 60, defaultPreferences.monitorRefreshSeconds))}
            />
          </div>
        </section>
        <section className="settings-panel danger-zone">
          <h2>Danger Zone</h2>
          <div className="setting-row">
            <div>
              <strong>Reset AGX database</strong>
              <span>Delete all registered projects, tasks, runtime state, log streams, and AGX tmux sessions.</span>
            </div>
            <button className="danger-button" disabled={busy} onClick={() => setConfirmingReset(true)}>
              Reset Database
            </button>
          </div>
        </section>
      </section>
      {confirmingReset && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingReset(false)}>
          <section className="confirm-modal danger-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Reset AGX Database</h2>
            <p>This deletes every AGX project and task from the local database, stops AGX tmux sessions, and clears task log streams. Repository files are not deleted.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingReset(false)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={resetDatabase}>Reset Everything</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </main>
  );
}

function ProjectView({
  projects,
  error,
  candidateLimit,
  openProjectAfterAdd,
  onRefresh,
  onOpenProject,
  theme,
  onToggleTheme,
}: {
  projects: Project[];
  error: string;
  candidateLimit: number;
  openProjectAfterAdd: boolean;
  onRefresh: () => void;
  onOpenProject: (project: Project) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Project | null>(null);
  const [deleting, setDeleting] = useState<Project | null>(null);
  const [editName, setEditName] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const gridRef = useRef<HTMLElement>(null);

  useEffect(() => {
    setSelectedIndex((value) => Math.min(Math.max(value, 0), Math.max(projects.length - 1, 0)));
  }, [projects.length]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (adding || editing || deleting || projects.length === 0 || isTextEntry(event.target)) return;
      const columns = projectGridColumns(gridRef.current);
      if (event.altKey && event.key === 'Enter') {
        event.preventDefault();
        onOpenProject(projects[selectedIndex]);
        return;
      }
      switch (event.key) {
        case 'ArrowRight':
          event.preventDefault();
          setSelectedIndex((value) => Math.min(projects.length - 1, value + 1));
          break;
        case 'ArrowLeft':
          event.preventDefault();
          setSelectedIndex((value) => Math.max(0, value - 1));
          break;
        case 'ArrowDown':
          event.preventDefault();
          setSelectedIndex((value) => Math.min(projects.length - 1, value + columns));
          break;
        case 'ArrowUp':
          event.preventDefault();
          setSelectedIndex((value) => Math.max(0, value - columns));
          break;
        case 'Enter':
          event.preventDefault();
          onOpenProject(projects[selectedIndex]);
          break;
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [adding, deleting, editing, onOpenProject, projects, selectedIndex]);

  function startEdit(project: Project) {
    setEditing(project);
    setEditName(project.name);
    setEditDescription(project.description ?? '');
    setLocalError('');
  }

  async function saveEdit() {
    if (!editing || !editName.trim()) return;
    setBusy(true);
    setLocalError('');
    try {
      await api.UpdateProject(editing.id, editName, editDescription);
      setEditing(null);
      onRefresh();
    } catch (err) {
      setLocalError(String(err));
    } finally {
      setBusy(false);
    }
  }

  async function deleteProject() {
    if (!deleting) return;
    setBusy(true);
    setLocalError('');
    try {
      await api.DeleteProject(deleting.id);
      setDeleting(null);
      onRefresh();
    } catch (err) {
      setLocalError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="app-shell">
      <Header title="Projects" subtitle="Registered AGX workspaces" theme={theme} onToggleTheme={onToggleTheme}>
        <button className="primary-button" onClick={() => setAdding(true)}>
          Add Project
        </button>
        <IconButton label="Refresh projects" onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error || localError} />
      {projects.length === 0 ? (
        <EmptyState title="No projects" detail="Add a project to open a local git repository." />
      ) : (
        <section className="project-grid" ref={gridRef}>
          {projects.map((item, index) => (
            <article
              className={`project-card ${index === selectedIndex ? 'selected' : ''}`}
              key={item.id}
              tabIndex={0}
              style={{ animationDelay: `${Math.min(index * 35, 240)}ms` }}
              onClick={() => setSelectedIndex(index)}
              onFocus={() => setSelectedIndex(index)}
              onDoubleClick={() => onOpenProject(item)}
            >
              <Folder size={22} />
              <span className="card-title">{item.name}</span>
              <span className="path-text">{item.path}</span>
              {item.description && <span className="description-text">{item.description}</span>}
              <LanguageBars languages={item.languages ?? []} compact />
              <span className="metric-row">
                <strong>{item.taskCount}</strong> tasks
                <strong>{item.activeCount}</strong> active
                <strong>{item.waitingCount}</strong> waiting
              </span>
              <span className="project-actions" onClick={(event) => event.stopPropagation()} onDoubleClick={(event) => event.stopPropagation()}>
                <button className="text-button" onClick={() => onOpenProject(item)}>Open</button>
                <button className="text-button" onClick={() => startEdit(item)}>Edit</button>
                <button
                  className="danger-button"
                  onClick={(event) => {
                    event.stopPropagation();
                    setDeleting(item);
                  }}
                >
                  Delete
                </button>
              </span>
            </article>
          ))}
        </section>
      )}
      {adding && (
        <AddProjectModal
          candidateLimit={candidateLimit}
          onCancel={() => setAdding(false)}
          onCreated={(created) => {
            setAdding(false);
            onRefresh();
            if (openProjectAfterAdd) onOpenProject(created);
          }}
        />
      )}
      {editing && (
        <div className="modal-backdrop" onMouseDown={() => setEditing(null)}>
          <section className="project-edit-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Edit Project</h2>
            <label>
              Project name
              <input value={editName} onChange={(event) => setEditName(event.target.value)} />
            </label>
            <label>
              Description
              <textarea value={editDescription} onChange={(event) => setEditDescription(event.target.value)} />
            </label>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setEditing(null)}>Cancel</button>
              <button className="primary-button" disabled={busy || !editName.trim()} onClick={saveEdit}>Save</button>
            </div>
          </section>
        </div>
      )}
      {deleting && (
        <div className="modal-backdrop blurred" onMouseDown={() => setDeleting(null)}>
          <section className="confirm-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Delete Project</h2>
            <p>Delete "{deleting.name}" from AGX? This stops sessions and removes task worktrees.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setDeleting(null)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={deleteProject}>Delete</button>
            </div>
          </section>
        </div>
      )}
    </main>
  );
}

function AddProjectModal({
  candidateLimit,
  onCancel,
  onCreated,
}: {
  candidateLimit: number;
  onCancel: () => void;
  onCreated: (project: Project) => void;
}) {
  const [candidates, setCandidates] = useState<ProjectCandidate[]>([]);
  const [selected, setSelected] = useState<ProjectCandidate | null>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [filter, setFilter] = useState('');
  const [manualPath, setManualPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [loadingCandidates, setLoadingCandidates] = useState(true);
  const [localError, setLocalError] = useState('');
  const nameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;
    setLoadingCandidates(true);
    api.ListProjectCandidates(candidateLimit)
      .then((items) => {
        if (!cancelled) setCandidates(items);
      })
      .catch((err) => {
        if (!cancelled) setLocalError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoadingCandidates(false);
      });
    return () => {
      cancelled = true;
    };
  }, [candidateLimit]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCancel();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onCancel]);

  useEffect(() => {
    if (selected) {
      window.setTimeout(() => nameRef.current?.focus(), 0);
    }
  }, [selected]);

  function chooseCandidate(candidate: ProjectCandidate) {
    setSelected(candidate);
    setName(candidate.name);
    setDescription(candidate.description ?? '');
    setManualPath(candidate.path);
    setLocalError('');
  }

  async function openBrowser() {
    setBusy(true);
    setLocalError('');
    try {
      let start = manualPath.trim() || selected?.path || '';
      if (!start) start = await api.HomeDirectory();
      const path = await api.SelectProjectDirectory(start);
      if (path.trim()) await usePath(path);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function usePath(path: string) {
    const target = path.trim();
    if (!target) return;
    setBusy(true);
    setLocalError('');
    try {
      const candidate = await api.ValidateProjectDirectory(target);
      chooseCandidate(candidate);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function createProject() {
    if (!selected || !name.trim()) return;
    setBusy(true);
    setLocalError('');
    try {
      const project = await api.RegisterProject(selected.path, name, description);
      onCreated(project);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const filteredCandidates = candidates.filter((candidate) => {
    const query = filter.trim().toLowerCase();
    if (!query) return true;
    return `${candidate.name} ${candidate.path}`.toLowerCase().includes(query);
  });

  return (
    <div
      className="modal-backdrop blurred"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <section className="project-add-modal" onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-header">
          <div>
            <h2>Add Git Project</h2>
            <p>Pick a discovered repository or browse to a local Git checkout.</p>
          </div>
          <IconButton label="Close" onClick={onCancel}>
            <X size={18} />
          </IconButton>
        </header>
        {localError && (
          <div className="modal-error">
            <span>{localError}</span>
          </div>
        )}
        <div className="project-add-grid">
          <section className="candidate-panel">
            <div className="candidate-toolbar">
              <label className="search-input">
                <Search size={15} />
                <input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Filter repositories" />
              </label>
              <button
                className="text-button icon-text-button"
                onMouseDown={(event) => event.stopPropagation()}
                onClick={openBrowser}
                disabled={busy}
              >
                <FolderOpen size={16} />
                Browse
              </button>
            </div>
            <div className="path-picker">
              <input
                value={manualPath}
                onChange={(event) => setManualPath(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void usePath(manualPath);
                }}
                placeholder="Paste any Git repository path under your home directory"
              />
              <button className="text-button" disabled={busy || !manualPath.trim()} onClick={() => usePath(manualPath)}>
                Use Path
              </button>
            </div>
            <div className="candidate-list">
              {loadingCandidates ? (
                <div className="candidate-empty">Scanning your home directory for Git repositories...</div>
              ) : filteredCandidates.length === 0 ? (
                <div className="candidate-empty">No unregistered Git repositories found.</div>
              ) : (
                filteredCandidates.map((candidate) => (
                  <button
                    className={`candidate-card ${selected?.path === candidate.path ? 'selected' : ''}`}
                    key={candidate.path}
                    onClick={() => usePath(candidate.path)}
                  >
                    <span className="candidate-title">
                      <Folder size={17} />
                      {candidate.name}
                    </span>
                    <span className="candidate-path">{candidate.path}</span>
                    <LanguageBars languages={candidate.languages ?? []} compact />
                  </button>
                ))
              )}
            </div>
          </section>
          <section className="project-details-panel">
            {selected ? (
              <>
                <div className="selected-repo-card">
                  <CheckCircle2 size={18} />
                  <div>
                    <strong>{selected.name}</strong>
                    <span>{selected.path}</span>
                  </div>
                </div>
                <LanguageBars languages={selected.languages ?? []} />
                <label>
                  Project name
                  <input ref={nameRef} value={name} onChange={(event) => setName(event.target.value)} placeholder="Project name" />
                </label>
                <label>
                  Description
                  <textarea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Optional project description" />
                </label>
              </>
            ) : (
              <div className="details-empty">
                <Code2 size={28} />
                <strong>Select a repository</strong>
                <span>AGX only accepts folders that pass Git repository validation.</span>
              </div>
            )}
          </section>
        </div>
        <footer className="wizard-actions">
          <button className="text-button" onClick={onCancel}>Cancel</button>
          <button className="primary-button" onClick={createProject} disabled={busy || !selected || !name.trim()}>
            Add Project
          </button>
        </footer>
      </section>
    </div>
  );
}

function LanguageBars({ languages, compact = false }: { languages: LanguageStat[]; compact?: boolean }) {
  if (languages.length === 0) {
    return compact ? null : <div className="language-empty">No language data yet</div>;
  }
  return (
    <div className={`language-block ${compact ? 'compact' : ''}`}>
      <div className="language-stack" aria-hidden="true">
        {languages.map((language) => (
          <span
            className={`language-slice ${languageClass(language.name)}`}
            key={language.name}
            style={{ width: `${Math.max(language.percentage, 3)}%` }}
          />
        ))}
      </div>
      <div className="language-list">
        {languages.map((language) => (
          <span key={language.name}>
            <i className={`language-dot ${languageClass(language.name)}`} />
            {language.name} {language.percentage}%
          </span>
        ))}
      </div>
    </div>
  );
}

function languageClass(name: string): string {
  switch (name) {
    case 'C++':
      return 'lang-cpp';
    case 'C#':
      return 'lang-csharp';
    default:
      return `lang-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`;
  }
}

function TaskView({
  project,
  tasks,
  viewMode,
  defaultAllMighty,
  discordConnected,
  busy,
  error,
  onBack,
  onRefresh,
  onViewMode,
  onSelectTask,
  onSplitTask,
  onAction,
  onLog,
  onProjectChanged,
  theme,
  onToggleTheme,
}: {
  project: Project;
  tasks: Task[];
  viewMode: ViewMode;
  defaultAllMighty: boolean;
  discordConnected: boolean;
  busy: boolean;
  error: string;
  onBack: () => void;
  onRefresh: () => void;
  onViewMode: (mode: ViewMode) => void;
  onSelectTask: (task: Task) => void;
  onSplitTask: (task: Task) => void;
  onAction: DesktopAction;
  onLog: (message: string) => void;
  onProjectChanged: (project: Project) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [agent, setAgent] = useState('');
  const [allMighty, setAllMighty] = useState(defaultAllMighty);
  const [workspaceMode, setWorkspaceMode] = useState<WorkspaceMode>('worktree');
  const [attachToDiscord, setAttachToDiscord] = useState(false);
  const [quickTemplate, setQuickTemplate] = useState<QuickTaskTemplate | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [leaving, setLeaving] = useState(false);
  const [focusedTaskID, setFocusedTaskID] = useState<string | null>(null);
  const [showTaskOutput, setShowTaskOutput] = useState(true);
  const [taskFilter, setTaskFilter] = useState<TaskInterfaceFilter>('all');
  const [grantingAccess, setGrantingAccess] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const taskCounts = useMemo(() => taskInterfaceCounts(tasks), [tasks]);
  const visibleTasks = useMemo(() => tasksForInterfaceFilter(tasks, taskFilter), [tasks, taskFilter]);
  const focusedTask = visibleTasks.find((task) => task.id === focusedTaskID) ?? null;

  useEffect(() => {
    void api.ListAvailableAgents(project.id).then(setAgents).catch(() => setAgents([]));
  }, [project.id]);

  useEffect(() => {
    setAllMighty(defaultAllMighty);
  }, [defaultAllMighty, project.id]);

  useEffect(() => {
    if (visibleTasks.length === 0) {
      setFocusedTaskID(null);
      return;
    }
    setFocusedTaskID((current) => (current && visibleTasks.some((task) => task.id === current) ? current : visibleTasks[0].id));
  }, [visibleTasks]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.altKey && event.key === 'Backspace') {
        event.preventDefault();
        onBack();
        return;
      }
      if (event.altKey && event.key.toLowerCase() === 't') {
        event.preventDefault();
        setShowTaskOutput((value) => !value);
        return;
      }
      if (!isTextEntry(event.target) && visibleTasks.length > 0) {
        const currentIndex = Math.max(0, visibleTasks.findIndex((task) => task.id === focusedTaskID));
        if (event.altKey && event.key === 'Enter') {
          event.preventDefault();
          onSelectTask(visibleTasks[currentIndex]);
          return;
        }
        const columns = viewMode === 'grid' ? projectGridColumns(document.querySelector<HTMLElement>('.task-grid')) : 1;
        const moves: Record<string, number> = {
          ArrowLeft: -1,
          ArrowRight: 1,
          ArrowUp: -columns,
          ArrowDown: columns,
        };
        if (event.key in moves) {
          event.preventDefault();
          const nextIndex = Math.min(visibleTasks.length - 1, Math.max(0, currentIndex + moves[event.key]));
          setFocusedTaskID(visibleTasks[nextIndex].id);
          return;
        }
        if (event.key === 'Enter') {
          event.preventDefault();
          onSelectTask(visibleTasks[currentIndex]);
          return;
        }
      }
      if (!(event.ctrlKey || event.metaKey)) return;
      switch (event.key) {
        case '1':
          event.preventDefault();
          onViewMode('grid');
          break;
        case '2':
          event.preventDefault();
          onViewMode('list');
          break;
        case 'n':
          event.preventDefault();
          titleRef.current?.focus();
          break;
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [focusedTaskID, onBack, onSelectTask, onViewMode, visibleTasks, viewMode]);

  function createTask() {
    if (!title.trim() || !project.accessGranted || (attachToDiscord && !discordConnected)) return;
    onAction(async () => {
      const task = attachToDiscord
        ? await api.CreateDiscordTask(project.id, title, description, agent, allMighty, workspaceMode)
        : await api.CreateTask(project.id, title, description, agent, allMighty, workspaceMode);
      setTaskFilter(attachToDiscord ? 'discord' : 'desktop');
      setTitle('');
      setDescription('');
      setAllMighty(defaultAllMighty);
      setWorkspaceMode('worktree');
      setAttachToDiscord(false);
      return { taskID: task.id, expectSession: !attachToDiscord };
    }, `create ${attachToDiscord ? 'Discord ' : ''}${workspaceMode} task "${title.trim()}"${allMighty ? ' all-mighty' : ''}`);
  }

  async function createQuickTask(template: QuickTaskTemplate, agentName: string, discordAttached: boolean, selectedWorkspaceMode: WorkspaceMode) {
    if (!project.accessGranted || (discordAttached && !discordConnected)) return;
    const created = await onAction(async () => {
      const task = discordAttached
        ? await api.CreateDiscordTask(project.id, template.title, template.prompt, agentName, allMighty, selectedWorkspaceMode)
        : template.prompt === ''
        ? await api.CreateTaskNoPrompt(project.id, template.title, agentName, allMighty, selectedWorkspaceMode)
        : await api.CreateTask(project.id, template.title, template.prompt, agentName, allMighty, selectedWorkspaceMode);
      setTaskFilter(discordAttached ? 'discord' : 'desktop');
      return { taskID: task.id, expectSession: !discordAttached };
    }, `quick ${discordAttached ? 'Discord ' : ''}${selectedWorkspaceMode} task "${template.title}"${agentName ? ` with ${agentName}` : ''}${allMighty ? ' all-mighty' : ''}`);
    if (created) {
      setQuickTemplate(null);
    }
  }

  async function grantAccess() {
    if (grantingAccess) return;
    setGrantingAccess(true);
    onLog(`$ grant access "${project.name}"`);
    try {
      const updated = await api.GrantProjectAccess(project.id);
      onProjectChanged(updated);
      onLog(`[ok] grant access "${project.name}"`);
    } catch (err) {
      const message = errorMessage(err);
      onLog(`[error] grant access "${project.name}": ${message}`);
    } finally {
      setGrantingAccess(false);
    }
  }

  function leaveToProjects() {
    setLeaving(true);
    window.setTimeout(onBack, 220);
  }

  return (
    <main className={`app-shell task-view ${leaving ? 'leaving' : ''}`}>
      <Header title={project.name} subtitle={project.path} theme={theme} onToggleTheme={onToggleTheme}>
        <IconButton label="Back to projects" onClick={leaveToProjects}>
          <ArrowLeft size={18} />
        </IconButton>
        <Segmented value={viewMode} onChange={onViewMode} />
        <IconButton label={showTaskOutput ? 'Hide task output' : 'Show task output'} onClick={() => setShowTaskOutput((value) => !value)}>
          <SquareTerminal size={18} />
        </IconButton>
        <IconButton label="Refresh tasks" onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      {busy && <div className="busy-bar">Working...</div>}
      <section className="task-toolbar">
        <input ref={titleRef} value={title} onChange={(event) => setTitle(event.target.value)} placeholder="Task title" />
        <input value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Prompt or details" />
        <select value={agent} onChange={(event) => setAgent(event.target.value)} aria-label="Agent">
          <option value="">Default agent</option>
          {agents.map((item) => (
            <option key={item.name} value={item.name}>
              {item.name}{item.available ? '' : ' (missing)'}
            </option>
          ))}
        </select>
        <label className={`all-mighty-toggle ${allMighty ? 'active' : ''}`} title="Run without approval prompts or sandbox restrictions where the agent supports it">
          <input type="checkbox" checked={allMighty} onChange={(event) => setAllMighty(event.target.checked)} />
          <ShieldCheck size={16} />
          <span>All-mighty</span>
        </label>
        <div className="workspace-mode-toggle" role="group" aria-label="Workspace mode">
          <button type="button" className={workspaceMode === 'worktree' ? 'active' : ''} onClick={() => setWorkspaceMode('worktree')} title="Run in an isolated task worktree">
            <GitBranch size={15} />
            <span>Worktree</span>
          </button>
          <button type="button" className={workspaceMode === 'project' ? 'active' : ''} onClick={() => setWorkspaceMode('project')} title="Run in the current project checkout. Only one project-mode task can be active.">
            <FolderOpen size={15} />
            <span>Project</span>
          </button>
        </div>
        <label className={`discord-attach-toggle ${attachToDiscord ? 'active' : ''}`} title={discordConnected ? 'Create this task as Discord-controlled. Desktop input will be read-only.' : 'Connect Discord before creating Discord-controlled tasks.'}>
          <input type="checkbox" checked={attachToDiscord} onChange={(event) => setAttachToDiscord(event.target.checked)} />
          <MessageCircle size={16} />
          <span>Attach to Discord</span>
        </label>
        <button className="primary-button" onClick={createTask} disabled={busy || !project.accessGranted || !title.trim() || (attachToDiscord && !discordConnected)}>
          Create
        </button>
      </section>
      <section className="quick-task-row" aria-label="Quick task templates">
        <div className="quick-task-buttons">
          {quickTaskTemplates.map((template) => (
            <button className="quick-task-button" key={template.id} disabled={busy || !project.accessGranted || (attachToDiscord && !discordConnected)} onClick={() => setQuickTemplate(template)}>
              {template.id === 'vanilla' && <SquareTerminal size={16} />}
              {template.id === 'coding-machine' && <Code2 size={16} />}
              {template.id === 'code-reviewer' && <CheckCircle2 size={16} />}
              {template.id === 'planner' && <List size={16} />}
              <span>{template.label}</span>
            </button>
          ))}
        </div>
        <div className={`project-access-inline ${project.accessGranted ? 'granted' : 'required'}`}>
          {!project.accessGranted && <span className="project-access-state">Access Required</span>}
          <button className="text-button project-access-button" onClick={() => void grantAccess()} disabled={grantingAccess} title={project.accessGranted ? 'Re-run project access repair and validation' : 'Grant project write access before creating tasks'}>
            {project.accessGranted && <CheckCircle2 size={15} />}
            {!project.accessGranted && <LockKeyhole size={15} />}
            {grantingAccess ? 'Granting...' : project.accessGranted ? 'Access Granted' : 'Grant Access'}
          </button>
        </div>
      </section>
      <TaskInterfaceTabs value={taskFilter} counts={taskCounts} onChange={setTaskFilter} />
      <section className={`task-board-layout ${showTaskOutput ? 'with-output' : ''}`}>
        <section className="task-board-main">
          {visibleTasks.length === 0 ? (
            <EmptyState title={tasks.length === 0 ? 'No tasks' : `No ${taskInterfaceLabel(taskFilter)} tasks`} detail={tasks.length === 0 ? 'Create a task to start an agent session.' : 'Switch tabs or create a matching task.'} />
          ) : viewMode === 'list' ? (
            <TaskList tasks={visibleTasks} busy={busy} focusedTaskID={focusedTaskID} onFocusTask={setFocusedTaskID} onSelectTask={onSelectTask} onSplitTask={onSplitTask} onAction={onAction} />
          ) : (
            <section className="task-grid">
              {visibleTasks.map((task, index) => (
                <TaskCard key={task.id} task={task} busy={busy} focused={task.id === focusedTaskID} onFocus={() => setFocusedTaskID(task.id)} onOpen={() => onSelectTask(task)} onAction={onAction} index={index} />
              ))}
            </section>
          )}
        </section>
        {showTaskOutput && (
          <TaskOutputPanel
            task={focusedTask}
            theme={theme}
            onOpenTask={() => focusedTask && onSelectTask(focusedTask)}
          />
        )}
      </section>
      {quickTemplate && (
        <QuickTaskModal
          template={quickTemplate}
          agents={agents}
          busy={busy}
          allMighty={allMighty}
          initialWorkspaceMode={workspaceMode}
          initialAttachToDiscord={attachToDiscord}
          discordConnected={discordConnected}
          onCancel={() => setQuickTemplate(null)}
          onCreate={(agentName, discordAttached, selectedWorkspaceMode) => void createQuickTask(quickTemplate, agentName, discordAttached, selectedWorkspaceMode)}
        />
      )}
      {!project.accessGranted && (
        <GrantAccessModal
          project={project}
          granting={grantingAccess}
          onBack={leaveToProjects}
          onGrant={() => void grantAccess()}
        />
      )}
    </main>
  );
}

function TaskInterfaceTabs({ value, counts, onChange }: { value: TaskInterfaceFilter; counts: Record<TaskInterfaceFilter, number>; onChange: (value: TaskInterfaceFilter) => void }) {
  const tabs: Array<{ value: TaskInterfaceFilter; label: string; icon: React.ReactNode }> = [
    { value: 'all', label: 'All', icon: <Grid2X2 size={15} /> },
    { value: 'desktop', label: 'Desktop', icon: <SquareTerminal size={15} /> },
    { value: 'discord', label: 'Discord', icon: <MessageCircle size={15} /> },
  ];
  return (
    <nav className="task-interface-tabs" aria-label="Task type filter">
      {tabs.map((tab) => (
        <button key={tab.value} type="button" className={value === tab.value ? 'active' : ''} onClick={() => onChange(tab.value)} aria-pressed={value === tab.value}>
          {tab.icon}
          <span>{tab.label}</span>
          <strong>{counts[tab.value]}</strong>
        </button>
      ))}
    </nav>
  );
}

function GrantAccessModal({
  project,
  granting,
  onBack,
  onGrant,
}: {
  project: Project;
  granting: boolean;
  onBack: () => void;
  onGrant: () => void;
}) {
  return (
    <div className="modal-backdrop blurred access-backdrop">
      <section className="project-access-modal" role="dialog" aria-modal="true" aria-labelledby="grant-access-title">
        <header className="modal-header">
          <div>
            <h2 id="grant-access-title">Grant Access</h2>
            <p>{project.name}</p>
          </div>
        </header>
        <div className="access-modal-body">
          <p>AGX needs to verify that it can create and write task worktrees for this project before agents run.</p>
          <code>{project.path}</code>
          {project.accessError && <div className="access-modal-error">{project.accessError}</div>}
        </div>
        <footer className="wizard-actions">
          <button className="text-button" disabled={granting} onClick={onBack}>
            Back
          </button>
          <button className="primary-button" disabled={granting} onClick={onGrant}>
            {granting ? 'Granting...' : 'Grant Access'}
          </button>
        </footer>
      </section>
    </div>
  );
}

function ActionErrorDialog({ title, message, onClose }: { title: string; message: string; onClose: () => void }) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  return (
    <div className="modal-backdrop blurred" onMouseDown={onClose}>
      <section className="action-error-modal" role="alertdialog" aria-modal="true" aria-labelledby="action-error-title" onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-header">
          <div>
            <h2 id="action-error-title">Action Failed</h2>
            <p>{title}</p>
          </div>
          <IconButton label="Close" onClick={onClose}>
            <X size={18} />
          </IconButton>
        </header>
        <div className="modal-error">
          <span>{message}</span>
        </div>
        <footer className="wizard-actions">
          <button className="primary-button" onClick={onClose}>OK</button>
        </footer>
      </section>
    </div>
  );
}

function QuickTaskModal({
  template,
  agents,
  busy,
  allMighty,
  initialWorkspaceMode,
  initialAttachToDiscord,
  discordConnected,
  onCancel,
  onCreate,
}: {
  template: QuickTaskTemplate;
  agents: Agent[];
  busy: boolean;
  allMighty: boolean;
  initialWorkspaceMode: WorkspaceMode;
  initialAttachToDiscord: boolean;
  discordConnected: boolean;
  onCancel: () => void;
  onCreate: (agentName: string, discordAttached: boolean, workspaceMode: WorkspaceMode) => void;
}) {
  const availableAgents = agents.filter((agent) => agent.available);
  const [workspaceMode, setWorkspaceMode] = useState<WorkspaceMode>(initialWorkspaceMode);
  const [attachToDiscord, setAttachToDiscord] = useState(initialAttachToDiscord);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCancel();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onCancel]);

  return (
    <div className="modal-backdrop blurred" onMouseDown={onCancel}>
      <section className="quick-task-modal" onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-header">
          <div>
            <h2>{template.label}</h2>
            <p>Select an agent to start this prepared task.</p>
          </div>
          <IconButton label="Close" onClick={onCancel}>
            <X size={18} />
          </IconButton>
        </header>
        <label className={`quick-discord-choice ${attachToDiscord ? 'active' : ''}`} title={discordConnected ? 'Create this quick task as Discord-controlled.' : 'Connect Discord before creating Discord-controlled tasks.'}>
          <input
            type="checkbox"
            checked={attachToDiscord}
            disabled={!discordConnected}
            onChange={(event) => setAttachToDiscord(event.target.checked)}
          />
          <MessageCircle size={16} />
          <span>Attach to Discord</span>
        </label>
        <div className="quick-workspace-choice workspace-mode-toggle" role="group" aria-label="Workspace mode">
          <button type="button" className={workspaceMode === 'worktree' ? 'active' : ''} onClick={() => setWorkspaceMode('worktree')} title="Run in an isolated task worktree">
            <GitBranch size={15} />
            <span>Worktree</span>
          </button>
          <button type="button" className={workspaceMode === 'project' ? 'active' : ''} onClick={() => setWorkspaceMode('project')} title="Run in the current project checkout. Only one project-mode task can be active.">
            <FolderOpen size={15} />
            <span>Project</span>
          </button>
        </div>
        <div className="quick-agent-list">
          <button className="agent-choice-button" disabled={busy || (attachToDiscord && !discordConnected)} onClick={() => onCreate('', attachToDiscord, workspaceMode)}>
            <span className="agent-choice-title">
              <strong>Default agent</strong>
              <WorkspaceBadge mode={workspaceMode} />
              {allMighty && <AllMightyBadge />}
              {attachToDiscord && <DiscordBadge />}
            </span>
            <span className="agent-choice-detail">Use this project's configured default</span>
          </button>
          {availableAgents.map((agent) => (
            <button className="agent-choice-button" key={agent.name} disabled={busy || (attachToDiscord && !discordConnected)} onClick={() => onCreate(agent.name, attachToDiscord, workspaceMode)}>
              <span className="agent-choice-title">
                <strong>{agentLabel(agent.name)}</strong>
                <WorkspaceBadge mode={workspaceMode} />
                {allMighty && <AllMightyBadge />}
                {attachToDiscord && <DiscordBadge />}
              </span>
              <span className="agent-choice-detail">{agent.description || agent.command}</span>
            </button>
          ))}
        </div>
        <footer className="wizard-actions">
          <button className="text-button" onClick={onCancel}>Cancel</button>
        </footer>
      </section>
    </div>
  );
}

function TaskOutputPanel({
  task,
  theme,
  onOpenTask,
}: {
  task: Task | null;
  theme: ThemeMode;
  onOpenTask: () => void;
}) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const fitAddon = useRef<FitAddon | null>(null);
  const lastLogs = useRef('');
  const hasSession = hasTmuxSession(task);
  const hasStructured = hasStructuredSession(task);

  useEffect(() => {
    if (!terminalRef.current) return;
    terminal.current = new Terminal({
      convertEol: true,
      disableStdin: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 12,
      theme: terminalTheme(theme),
    });
    fitAddon.current = new FitAddon();
    terminal.current.loadAddon(fitAddon.current);
    terminal.current.open(terminalRef.current);
    fitAddon.current.fit();
    const observer = new ResizeObserver(() => fitAddon.current?.fit());
    observer.observe(terminalRef.current);
    return () => {
      observer.disconnect();
      terminal.current?.dispose();
      terminal.current = null;
      fitAddon.current = null;
    };
  }, []);

  useEffect(() => {
    if (!terminal.current) return;
    terminal.current.options.theme = terminalTheme(theme);
  }, [theme]);

  useEffect(() => {
    fitAddon.current?.fit();
  }, [task?.id]);

  useEffect(() => {
    lastLogs.current = '';
    terminal.current?.clear();
    if (!task) {
      terminal.current?.write('No task selected.\r\n');
      return;
    }
    if (!hasSession && hasStructured) {
      terminal.current?.write(structuredSessionMessage(task));
      return;
    }
    if (!hasSession) {
      terminal.current?.write(`No active session for "${task.title}".\r\nOpen the task and run it to start ${agentLabel(task.agent)}.\r\n`);
      return;
    }
    const unsubscribe = window.runtime?.EventsOn?.(`agx:logs:${task.id}`, (payload) => {
      const event = payload as LogEvent;
      if (event.error) {
        terminal.current?.clear();
        terminal.current?.write(event.error);
        return;
      }
      const data = event.data ?? '';
      if (event.reset) {
        terminal.current?.clear();
        lastLogs.current = data;
        terminal.current?.write(data.replace(/\n/g, '\r\n'));
        return;
      }
      lastLogs.current += data;
      terminal.current?.write(data.replace(/\n/g, '\r\n'));
    });
    void api.StartLogStream(task.id, 500).catch((err) => {
      terminal.current?.clear();
      terminal.current?.write(String(err));
    });
    return () => {
      unsubscribe?.();
      void api.StopLogStream(task.id).catch(() => {});
    };
  }, [task?.id, task?.title, task?.agent, hasSession, hasStructured]);

  return (
    <aside className="task-output-panel" aria-label="Focused task output">
      <header className="task-output-header">
        <div>
          <strong>{task?.title ?? 'Task output'}</strong>
          <span>{task ? `${agentLabel(task.agent)} · ${statusLabel(task.status)}` : 'No task selected'}</span>
        </div>
        <button className="text-button" disabled={!task} onClick={onOpenTask}>
          Open
        </button>
      </header>
      <div className="task-output-terminal" ref={terminalRef} onMouseDown={() => terminal.current?.focus()} />
    </aside>
  );
}

function TaskCard({ task, busy, focused, onFocus, onOpen, onAction, index = 0 }: { task: Task; busy: boolean; focused: boolean; onFocus: () => void; onOpen: () => void; onAction: DesktopAction; index?: number }) {
  return (
    <article
      className={`task-card ${focused ? 'focused' : ''}`}
      style={{ animationDelay: `${Math.min(index * 30, 240)}ms` }}
      onClick={onFocus}
    >
      <button className="task-open" onClick={onOpen}>
        <span className="card-title">{task.title}</span>
        <span className="task-badge-row">
          {isDiscordTask(task) && <DiscordBadge />}
          <WorkspaceBadge mode={task.workspaceMode} />
          {task.allMighty && <AllMightyBadge />}
          <AgentBadge agent={task.agent} />
        </span>
        <span className="task-activity">Last activity {relativeTime(task.updatedAt)}</span>
        {task.lastUserPrompt && <span className="task-last-prompt">{task.lastUserPrompt}</span>}
      </button>
      <span className={`status-pill task-status-pin ${task.status}`}>{statusLabel(task.status)}</span>
      <TaskActions task={task} busy={busy} onAction={onAction} />
    </article>
  );
}

function TaskList({ tasks, busy, focusedTaskID, onFocusTask, onSelectTask, onSplitTask, onAction }: { tasks: Task[]; busy: boolean; focusedTaskID: string | null; onFocusTask: (taskID: string) => void; onSelectTask: (task: Task) => void; onSplitTask: (task: Task) => void; onAction: DesktopAction }) {
  return (
    <section className="task-table">
      {tasks.map((task) => (
        <div
          className={`task-row ${task.id === focusedTaskID ? 'focused' : ''}`}
          key={task.id}
          onClick={() => onFocusTask(task.id)}
        >
          <button onClick={() => onSelectTask(task)}>{task.title}</button>
          <span>{statusLabel(task.status)}</span>
          <span><AgentBadge agent={task.agent} /></span>
          <span className="task-list-badges">{isDiscordTask(task) && <DiscordBadge />}<WorkspaceBadge mode={task.workspaceMode} />{task.allMighty ? <AllMightyBadge /> : 'Standard'}</span>
          <span>{relativeTime(task.updatedAt)}</span>
          <TaskActions task={task} busy={busy} onAction={onAction} />
        </div>
      ))}
    </section>
  );
}

function TaskActions({ task, busy, onAction }: { task: Task; busy: boolean; onAction: DesktopAction }) {
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(task.title);

  useEffect(() => {
    if (!editing) setTitle(task.title);
  }, [editing, task.title]);

  function deleteTask() {
    setConfirmingDelete(false);
    onAction(() => api.DeleteTask(task.id), `delete task "${task.title}"`);
  }

  function updateTitle() {
    const nextTitle = title.trim();
    if (!nextTitle || nextTitle === task.title) {
      setEditing(false);
      return;
    }
    setEditing(false);
    onAction(async () => {
      await api.UpdateTaskTitle(task.id, nextTitle);
    }, `rename task "${task.title}" to "${nextTitle}"`);
  }

  return (
    <>
      <div className="icon-row">
        {!isDiscordTask(task) && task.status === 'offline' && (
          <IconButton label="Restart task" disabled={busy} onClick={() => onAction(async () => {
            await api.RunTask(task.id);
            return { taskID: task.id, expectSession: true };
          }, `restart task "${task.title}"`)}>
            <Play size={16} />
          </IconButton>
        )}
        {!isDiscordTask(task) && (task.status === 'active' || task.status === 'waiting' || task.status === 'complete') && (
          <IconButton label="Stop task" disabled={busy} onClick={() => onAction(() => api.StopTask(task.id), `stop task "${task.title}"`)}>
            <Square size={16} />
          </IconButton>
        )}
        <button className="text-button" disabled={busy} onClick={() => setEditing(true)}>
          Edit
        </button>
        <IconButton label="Delete task" disabled={busy} onClick={() => setConfirmingDelete(true)}>
          <Trash2 size={16} />
        </IconButton>
      </div>
      {editing && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setEditing(false)}>
          <section className="confirm-modal task-edit-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Edit Task</h2>
            <label>
              Task name
              <input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    updateTitle();
                  }
                  if (event.key === 'Escape') {
                    event.preventDefault();
                    setEditing(false);
                  }
                }}
                autoFocus
              />
            </label>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setEditing(false)}>Cancel</button>
              <button className="primary-button" disabled={busy || !title.trim()} onClick={updateTitle}>Save</button>
            </div>
          </section>
        </div>
      ), document.body)}
      {confirmingDelete && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingDelete(false)}>
          <section className="confirm-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Delete Task</h2>
            <p>Delete "{task.title}" from AGX? This stops its session and removes its task worktree when AGX can do that safely.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingDelete(false)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={deleteTask}>Delete</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </>
  );
}

function SessionView({
  project,
  task,
  onBack,
  onError,
  onLog,
  onChanged,
  error,
  theme,
  onToggleTheme,
}: {
  project: Project;
  task: Task;
  onBack: () => void;
  onError: (error: string) => void;
  onLog: (message: string) => void;
  onChanged: () => Promise<void> | void;
  error: string;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [prompt, setPrompt] = useState('');
  const [contextPaths, setContextPaths] = useState<string[]>([]);
  const [includeFileContents, setIncludeFileContents] = useState(false);
  const [previewPath, setPreviewPath] = useState('');
  const [previewContent, setPreviewContent] = useState('');
  const [previewLoading, setPreviewLoading] = useState(false);
  const [renderPreviewMarkdown, setRenderPreviewMarkdown] = useState(false);
  const [showFilePanel, setShowFilePanel] = useState(true);
  const [filePanelWidth, setFilePanelWidth] = useState(280);
  const [promptHeightPercent, setPromptHeightPercent] = useState(15);
  const [activePane, setActivePane] = useState<'session' | 'preview'>('session');
  const discordOwned = isDiscordTask(task);
  const hasSession = hasTmuxSession(task);
  const hasStructured = hasStructuredSession(task);
  const canMessage = !discordOwned && hasSession;
  const terminalRef = useRef<HTMLDivElement>(null);
  const promptRef = useRef<HTMLTextAreaElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const fitAddon = useRef<FitAddon | null>(null);
  const lastLogs = useRef('');
  const taskIDRef = useRef(task.id);
  const hasSessionRef = useRef(hasSession);
  const onErrorRef = useRef(onError);

  taskIDRef.current = task.id;
  hasSessionRef.current = hasSession;
  onErrorRef.current = onError;

  useEffect(() => {
    if (!terminalRef.current) return;
    terminal.current = new Terminal({
      convertEol: false,
      disableStdin: false,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 13,
      theme: terminalTheme(theme),
    });
    fitAddon.current = new FitAddon();
    terminal.current.loadAddon(fitAddon.current);
    terminal.current.open(terminalRef.current);
    const syncSize = () => {
      fitAddon.current?.fit();
      const term = terminal.current;
      if (term && term.rows > 1) {
        term.resize(term.cols, term.rows - 1);
      }
      if (term && hasSessionRef.current) {
        void api.ResizeTaskTerminal(taskIDRef.current, term.cols, term.rows).catch((err) => onErrorRef.current(String(err)));
      }
    };
    syncSize();
    requestAnimationFrame(syncSize);
    const dataDisposable = terminal.current.onData((data) => {
      const taskID = taskIDRef.current;
      if (!taskID || !hasSessionRef.current) return;
      void api.SendInput(taskID, data).catch((err) => onErrorRef.current(String(err)));
    });
    const observer = new ResizeObserver(syncSize);
    observer.observe(terminalRef.current);
    return () => {
      observer.disconnect();
      dataDisposable.dispose();
      terminal.current?.dispose();
      terminal.current = null;
      fitAddon.current = null;
    };
  }, []);

  useEffect(() => {
    if (!terminal.current) return;
    terminal.current.options.theme = terminalTheme(theme);
  }, [theme]);

  useEffect(() => {
    if (!canMessage) return;
    setActivePane('session');
    const frame = requestAnimationFrame(() => promptRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [canMessage, task.id]);

  useEffect(() => {
    if (activePane === 'session') {
      fitAddon.current?.fit();
      const term = terminal.current;
      if (term && term.rows > 1) {
        term.resize(term.cols, term.rows - 1);
      }
      if (term && hasSession) {
        void api.ResizeTaskTerminal(task.id, term.cols, term.rows).catch((err) => onErrorRef.current(String(err)));
      }
    }
  }, [activePane, filePanelWidth, showFilePanel, promptHeightPercent, task.id, hasSession]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.altKey && event.key === 'Backspace') {
        event.preventDefault();
        event.stopPropagation();
        onBack();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'b') {
        event.preventDefault();
        setShowFilePanel((value) => !value);
      }
      if ((event.ctrlKey || event.metaKey) && event.key === 'Tab') {
        event.preventDefault();
        setActivePane((value) => (value === 'session' && previewPath ? 'preview' : 'session'));
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [onBack, previewPath]);

  useEffect(() => {
    const onMouseDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null;
      if (!target?.closest('.terminal-panel')) {
        terminal.current?.blur();
      }
    };
    const onWindowBlur = () => terminal.current?.blur();
    window.addEventListener('mousedown', onMouseDown, true);
    window.addEventListener('blur', onWindowBlur);
    return () => {
      window.removeEventListener('mousedown', onMouseDown, true);
      window.removeEventListener('blur', onWindowBlur);
    };
  }, []);

  useEffect(() => {
    lastLogs.current = '';
    terminal.current?.clear();
    if (!hasSession && hasStructured) {
      terminal.current?.write(structuredSessionMessage(task));
      return;
    }
    if (!hasSession) {
      terminal.current?.write(`No active session for "${task.title}".\r\nRun the task to start ${agentLabel(task.agent)}.\r\n`);
      return;
    }
    const unsubscribe = window.runtime?.EventsOn?.(`agx:logs:${task.id}`, (payload) => {
      const event = payload as LogEvent;
      if (event.error) {
        terminal.current?.clear();
        terminal.current?.write(event.error);
        return;
      }
      const data = event.data ?? '';
      if (event.reset) {
        terminal.current?.clear();
        lastLogs.current = data;
        terminal.current?.write(data.replace(/\r?\n/g, '\r\n'));
        return;
      }
      lastLogs.current += data;
      terminal.current?.write(data);
    });
    void api.StartLogStream(task.id, 600).catch((err) => {
      terminal.current?.clear();
      terminal.current?.write(String(err));
    });
    return () => {
      unsubscribe?.();
      void api.StopLogStream(task.id).catch(() => {});
    };
  }, [task.id, task.title, task.agent, hasSession, hasStructured]);

  async function runTask() {
    try {
      onLog(`$ run task "${task.title}"`);
      await api.RunTask(task.id);
      onLog(`[ok] run task "${task.title}"`);
      await onChanged();
      onError('');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] run task "${task.title}": ${message}`);
    }
  }

  async function sendPrompt() {
    const message = prompt.trim();
    if (!message) return;
    if (!canMessage) {
      onError('Task has no active session. Run it first.');
      return;
    }
    try {
      if (isAgentContextClearCommand(message)) {
        await api.SendMessage(task.id, message);
        setContextPaths([]);
      } else {
        const composed = await api.ComposeTaskPromptWithFiles(task.id, message, contextPaths, includeFileContents);
        await api.SendMessage(task.id, composed);
        await api.RecordTaskInput(task.id, message);
      }
      setPrompt('');
      onChanged();
    } catch (err) {
      onError(String(err));
    }
  }

  function startFileResize(event: React.MouseEvent<HTMLDivElement>) {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = filePanelWidth;
    const onMove = (moveEvent: MouseEvent) => {
      const maxWidth = Math.max(220, Math.min(520, window.innerWidth - 380));
      setFilePanelWidth(Math.min(maxWidth, Math.max(180, startWidth + moveEvent.clientX - startX)));
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  function resizePrompt(delta: number) {
    setPromptHeightPercent((value) => Math.min(60, Math.max(10, value + delta)));
  }

  if (discordOwned) {
    return (
      <DiscordTaskDetail
        project={project}
        task={task}
        onBack={onBack}
        onError={onError}
        onLog={onLog}
        onChanged={onChanged}
        error={error}
        theme={theme}
        onToggleTheme={onToggleTheme}
      />
    );
  }

  return (
    <main className="session-shell">
      <Header
        title={`${project.name} / ${task.title}`}
        subtitle={`${agentLabel(task.agent)} · ${statusLabel(task.status)}`}
        detail={task.worktreePath ?? project.path}
        theme={theme}
        onToggleTheme={onToggleTheme}
      >
        {!hasSession && !hasStructured && task.status === 'offline' && (
          <IconButton label="Run task" onClick={runTask}>
            <Play size={18} />
          </IconButton>
        )}
        <IconButton label={showFilePanel ? 'Hide file tree' : 'Show file tree'} onClick={() => setShowFilePanel((value) => !value)}>
          {showFilePanel ? <PanelLeftClose size={18} /> : <PanelLeftOpen size={18} />}
        </IconButton>
        <IconButton label="Back to tasks" onClick={onBack}>
          <ArrowLeft size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      <section className="session-layout" style={{ gridTemplateColumns: showFilePanel ? `${filePanelWidth}px 6px minmax(0, 1fr)` : 'minmax(0, 1fr)' }}>
        {showFilePanel && (
          <FilePanel
            project={project}
            taskId={task.id}
            rootPath={task.worktreePath ?? project.path}
            onInsertPaths={(paths) => setPrompt((value) => appendPromptPaths(value, paths))}
            onContextPaths={(paths) => setContextPaths((value) => addUniquePaths(value, paths))}
            onPreview={async (path) => {
              setPreviewPath(path);
              setPreviewContent('');
              setRenderPreviewMarkdown(isMarkdownPreviewPath(path));
              setPreviewLoading(true);
              setActivePane('preview');
              try {
                setPreviewContent(await api.ReadTaskFile(task.id, path));
              } catch (err) {
                setPreviewContent(String(err));
              } finally {
                setPreviewLoading(false);
              }
            }}
          />
        )}
        {showFilePanel && <div className="file-resizer" onMouseDown={startFileResize} />}
        <section className="workspace-panel">
          <div className="workspace-tabs">
            <button className={activePane === 'session' ? 'active' : ''} onClick={() => setActivePane('session')}>Session</button>
            <button className={activePane === 'preview' ? 'active' : ''} onClick={() => setActivePane('preview')} disabled={!previewPath}>Preview</button>
          </div>
          <section
            className={`terminal-panel ${activePane === 'session' ? '' : 'pane-hidden'}`}
            style={{ gridTemplateRows: `minmax(0, 1fr) auto minmax(96px, ${promptHeightPercent}%)` }}
          >
            <div
              className="terminal-host"
              ref={terminalRef}
              onMouseDown={() => terminal.current?.focus()}
            />
            <div
              className="context-bar"
              onDrop={(event) => {
                event.preventDefault();
                const paths = pathsFromDrop(event);
                if (paths.length > 0) setContextPaths((value) => addUniquePaths(value, paths));
              }}
              onDragOver={(event) => event.preventDefault()}
            >
              {contextPaths.map((path) => (
                <button key={path} aria-label={`Remove ${path} from context`} onClick={() => setContextPaths((value) => value.filter((item) => item !== path))}>
                  {path} x
                </button>
              ))}
              <label className="context-toggle">
                <input type="checkbox" checked={includeFileContents} onChange={(event) => setIncludeFileContents(event.target.checked)} />
                Include contents
              </label>
            </div>
            <div className="prompt-row" onDrop={(event) => {
              event.preventDefault();
              const paths = pathsFromDrop(event);
              if (paths.length > 0) setPrompt((value) => appendPromptPaths(value, paths));
            }} onDragOver={(event) => event.preventDefault()}>
              <div className="prompt-editor">
                <textarea
                  ref={promptRef}
                  value={prompt}
                  disabled={!canMessage}
                  onChange={(event) => setPrompt(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Escape') {
                      event.preventDefault();
                      event.stopPropagation();
                      focusSidebarNavigation();
                      return;
                    }
                    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
                      event.preventDefault();
                      void sendPrompt();
                    }
                  }}
                  placeholder={canMessage ? 'Message agent' : 'Run task to start a session'}
                />
                <div className="prompt-height-controls" aria-label="Prompt height controls">
                  <IconButton label="Decrease prompt height by 5%" disabled={promptHeightPercent <= 10} onClick={() => resizePrompt(-5)}>
                    <Minus size={16} />
                  </IconButton>
                  <IconButton label="Increase prompt height by 5%" disabled={promptHeightPercent >= 60} onClick={() => resizePrompt(5)}>
                    <Plus size={16} />
                  </IconButton>
                </div>
              </div>
              <IconButton label="Send message" disabled={!canMessage} onClick={sendPrompt}>
                <Send size={18} />
              </IconButton>
            </div>
          </section>
          <aside className={`preview-panel ${activePane === 'preview' ? '' : 'pane-hidden'}`}>
            {previewPath ? (
              <>
                <header>
                  <strong>{previewPath}</strong>
                  <div className="preview-panel-actions">
                    {isMarkdownPreviewPath(previewPath) && (
                      <button onClick={() => setRenderPreviewMarkdown((value) => !value)}>
                        {renderPreviewMarkdown ? 'Show Source' : 'Render Markdown'}
                      </button>
                    )}
                    <button onClick={() => { setPreviewPath(''); setRenderPreviewMarkdown(false); setActivePane('session'); }}>Close</button>
                  </div>
                </header>
                {previewLoading ? (
                  <div className="preview-empty">Loading preview...</div>
                ) : (
                  <CodePreview
                    path={previewPath}
                    content={previewContent}
                    renderMarkdown={renderPreviewMarkdown}
                    onAddContext={(reference) => setContextPaths((value) => addUniquePaths(value, [reference]))}
                  />
                )}
              </>
            ) : (
              <div className="preview-empty">No file selected</div>
            )}
          </aside>
        </section>
      </section>
    </main>
  );
}

function DiscordTaskDetail({
  project,
  task,
  onBack,
  onError,
  onLog,
  onChanged,
  error,
  theme,
  onToggleTheme,
}: {
  project: Project;
  task: Task;
  onBack: () => void;
  onError: (error: string) => void;
  onLog: (message: string) => void;
  onChanged: () => Promise<void> | void;
  error: string;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [messages, setMessages] = useState<TaskTranscriptMessage[]>([]);
  const [syncingDiscord, setSyncingDiscord] = useState(false);
  const [scrollState, setScrollState] = useState({ canScrollUp: false, canScrollDown: false, newBelow: 0 });
  const [showFilePanel, setShowFilePanel] = useState(true);
  const [filePanelWidth, setFilePanelWidth] = useState(280);
  const [activePane, setActivePane] = useState<'transcript' | 'preview'>('transcript');
  const [previewPath, setPreviewPath] = useState('');
  const [previewContent, setPreviewContent] = useState('');
  const [previewLoading, setPreviewLoading] = useState(false);
  const [renderPreviewMarkdown, setRenderPreviewMarkdown] = useState(false);
  const scrollRef = useRef<HTMLElement>(null);
  const autoFollowRef = useRef(true);
  const initializedScrollRef = useRef(false);
  const transcriptSignatureRef = useRef('');
  const pendingScrollRef = useRef<'bottom' | 'preserve' | null>(null);
  const previousScrollRef = useRef({ top: 0, height: 0 });

  const updateScrollState = useCallback((preserveNewBelow = false) => {
    const element = scrollRef.current;
    if (!element) return;
    const canScrollUp = element.scrollTop > 8;
    const canScrollDown = element.scrollHeight - element.scrollTop - element.clientHeight > 48;
    autoFollowRef.current = !canScrollDown;
    setScrollState((current) => {
      const newBelow = preserveNewBelow && canScrollDown ? current.newBelow : 0;
      if (current.canScrollUp === canScrollUp && current.canScrollDown === canScrollDown && current.newBelow === newBelow) {
        return current;
      }
      return { canScrollUp, canScrollDown, newBelow };
    });
  }, []);

  const scrollDiscordTranscript = useCallback((position: 'top' | 'bottom') => {
    const element = scrollRef.current;
    if (!element) return;
    const top = position === 'top' ? 0 : element.scrollHeight;
    element.scrollTo({ top, behavior: 'smooth' });
    if (position === 'bottom') {
      autoFollowRef.current = true;
      setScrollState((current) => ({ ...current, canScrollDown: false, newBelow: 0 }));
    } else {
      autoFollowRef.current = false;
    }
    window.setTimeout(updateScrollState, 180);
  }, [updateScrollState]);

  function startFileResize(event: React.MouseEvent<HTMLDivElement>) {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = filePanelWidth;
    const onMove = (moveEvent: MouseEvent) => {
      const maxWidth = Math.max(220, Math.min(520, window.innerWidth - 380));
      setFilePanelWidth(Math.min(maxWidth, Math.max(180, startWidth + moveEvent.clientX - startX)));
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  useEffect(() => {
    let cancelled = false;
    initializedScrollRef.current = false;
    autoFollowRef.current = true;
    transcriptSignatureRef.current = '';
    pendingScrollRef.current = 'bottom';
    previousScrollRef.current = { top: 0, height: 0 };
    setMessages([]);
    setScrollState({ canScrollUp: false, canScrollDown: false, newBelow: 0 });
    const load = async () => {
      try {
        const next = await api.ListTaskTranscript(task.id, 100);
        if (cancelled) return;
        const signature = transcriptMessagesSignature(next);
        const previousSignature = transcriptSignatureRef.current;
        if (signature === previousSignature) return;
        const element = scrollRef.current;
        const wasInitialized = initializedScrollRef.current;
        const shouldFollow = !wasInitialized || autoFollowRef.current;
        previousScrollRef.current = element ? { top: element.scrollTop, height: element.scrollHeight } : { top: 0, height: 0 };
        pendingScrollRef.current = shouldFollow ? 'bottom' : 'preserve';
        transcriptSignatureRef.current = signature;
        if (previousSignature && wasInitialized && !shouldFollow) {
          setScrollState((current) => ({ ...current, canScrollDown: true, newBelow: current.newBelow + 1 }));
        }
        setMessages(next);
      } catch {
        if (!cancelled) setMessages([]);
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [task.id]);

  useLayoutEffect(() => {
    let timeout: number | undefined;
    const frame = window.requestAnimationFrame(() => {
      const element = scrollRef.current;
      if (!element) return;
      const mode = pendingScrollRef.current;
      pendingScrollRef.current = null;
      if (!initializedScrollRef.current || mode === 'bottom') {
        element.scrollTo({ top: element.scrollHeight, behavior: initializedScrollRef.current ? 'smooth' : 'auto' });
        initializedScrollRef.current = true;
      } else if (mode === 'preserve') {
        const previous = previousScrollRef.current;
        const delta = element.scrollHeight - previous.height;
        if (delta > 0) {
          element.scrollTop = previous.top + delta;
        }
      }
      timeout = window.setTimeout(() => updateScrollState(mode === 'preserve'), mode === 'bottom' ? 180 : 0);
    });
    return () => {
      window.cancelAnimationFrame(frame);
      if (timeout !== undefined) window.clearTimeout(timeout);
    };
  }, [messages, updateScrollState]);

  const handleTranscriptScroll = useCallback(() => {
    updateScrollState();
  }, [updateScrollState]);

  async function syncWithDiscord() {
    if (syncingDiscord) return;
    setSyncingDiscord(true);
    onError('');
    onLog(`$ sync Discord task "${task.title}"`);
    try {
      await api.DiscordTaskSync(task.id);
      onLog(`[ok] sync Discord task "${task.title}"`);
      await onChanged();
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] sync Discord task "${task.title}": ${message}`);
    } finally {
      setSyncingDiscord(false);
    }
  }

  return (
    <main className="session-shell discord-task-detail-shell">
      <Header
        title={`${project.name} / ${task.title}`}
        subtitle={`${agentLabel(task.agent)} · ${statusLabel(task.status)} · Discord`}
        detail={task.worktreePath ?? project.path}
        theme={theme}
        onToggleTheme={onToggleTheme}
      >
        <button className="text-button" disabled={syncingDiscord} onClick={() => void syncWithDiscord()}>
          <RefreshCw size={15} />
          {syncingDiscord ? 'Syncing...' : 'Sync with Discord'}
        </button>
        <IconButton label={showFilePanel ? 'Hide file tree' : 'Show file tree'} onClick={() => setShowFilePanel((value) => !value)}>
          {showFilePanel ? <PanelLeftClose size={18} /> : <PanelLeftOpen size={18} />}
        </IconButton>
        <IconButton label="Back to tasks" onClick={onBack}>
          <ArrowLeft size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      <section className="session-layout" style={{ gridTemplateColumns: showFilePanel ? `${filePanelWidth}px 6px minmax(0, 1fr)` : 'minmax(0, 1fr)' }}>
        {showFilePanel && (
          <FilePanel
            project={project}
            taskId={task.id}
            rootPath={task.worktreePath ?? project.path}
            onInsertPaths={() => undefined}
            onContextPaths={() => undefined}
            onPreview={async (path) => {
              setPreviewPath(path);
              setPreviewContent('');
              setRenderPreviewMarkdown(isMarkdownPreviewPath(path));
              setPreviewLoading(true);
              setActivePane('preview');
              try {
                setPreviewContent(await api.ReadTaskFile(task.id, path));
              } catch (err) {
                setPreviewContent(String(err));
              } finally {
                setPreviewLoading(false);
              }
            }}
          />
        )}
        {showFilePanel && <div className="file-resizer" onMouseDown={startFileResize} />}
        <section className="workspace-panel">
          <div className="workspace-tabs">
            <button className={activePane === 'transcript' ? 'active' : ''} onClick={() => setActivePane('transcript')}>Transcript</button>
            <button className={activePane === 'preview' ? 'active' : ''} onClick={() => setActivePane('preview')} disabled={!previewPath}>Preview</button>
          </div>
          <section className={`discord-task-detail ${activePane === 'transcript' ? '' : 'pane-hidden'}`} ref={scrollRef} onScroll={handleTranscriptScroll}>
            <header>
              <DiscordBadge />
              <div>
                <h2>{task.title}</h2>
                <p>This task is controlled from Discord. Desktop input is read-only for this task.</p>
              </div>
            </header>
            {(scrollState.canScrollUp || scrollState.canScrollDown) && (
              <div className="discord-scroll-controls" aria-label="Discord transcript scroll controls">
                {scrollState.canScrollUp && (
                  <IconButton label="Scroll to top" onClick={() => scrollDiscordTranscript('top')}>
                    <ArrowUp size={17} />
                  </IconButton>
                )}
                {scrollState.canScrollDown && (
                  <IconButton label={scrollState.newBelow > 0 ? `Scroll to bottom (${scrollState.newBelow} new)` : 'Scroll to bottom'} onClick={() => scrollDiscordTranscript('bottom')}>
                    <span className="scroll-button-content">
                      <ArrowDown size={17} />
                      {scrollState.newBelow > 0 && <span className="scroll-new-count">{scrollState.newBelow}</span>}
                    </span>
                  </IconButton>
                )}
              </div>
            )}
            <section className="discord-transcript">
              {messages.length > 0 ? (
                messages.map((message) => (
                  <article className={`discord-message ${message.role}`} key={message.id}>
                    <span>{transcriptRoleLabel(message.role)}</span>
                    <TranscriptBody body={message.body} />
                  </article>
                ))
              ) : (
                <article className="discord-message status">
                  <span>Status</span>
                  <p>No Discord messages have been recorded for this task yet.</p>
                </article>
              )}
              <article className="discord-message status">
                <span>AGX</span>
                <p>Open the mapped Discord task channel to send messages. AGX Desktop will keep this task visible for status and review.</p>
              </article>
            </section>
          </section>
          <aside className={`preview-panel ${activePane === 'preview' ? '' : 'pane-hidden'}`}>
            {previewPath ? (
              <>
                <header>
                  <strong>{previewPath}</strong>
                  <div className="preview-panel-actions">
                    {isMarkdownPreviewPath(previewPath) && (
                      <button onClick={() => setRenderPreviewMarkdown((value) => !value)}>
                        {renderPreviewMarkdown ? 'Show Source' : 'Render Markdown'}
                      </button>
                    )}
                    <button onClick={() => { setPreviewPath(''); setRenderPreviewMarkdown(false); setActivePane('transcript'); }}>Close</button>
                  </div>
                </header>
                {previewLoading ? (
                  <div className="preview-empty">Loading preview...</div>
                ) : (
                  <CodePreview
                    path={previewPath}
                    content={previewContent}
                    renderMarkdown={renderPreviewMarkdown}
                  />
                )}
              </>
            ) : (
              <div className="preview-empty">No file selected</div>
            )}
          </aside>
        </section>
      </section>
    </main>
  );
}

function SplitView({ project, tasks, onBack, onRemove, onError, error, theme, onToggleTheme }: { project: Project; tasks: Task[]; onBack: () => void; onRemove: (id: string) => void; onError: (error: string) => void; error: string; theme: ThemeMode; onToggleTheme: () => void }) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onBack();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onBack]);

  return (
    <main className="app-shell">
      <Header title={`${project.name} split view`} subtitle={`${tasks.length} tasks`} theme={theme} onToggleTheme={onToggleTheme}>
        <IconButton label="Back to tasks" onClick={onBack}>
          <ArrowLeft size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      <section className="split-grid">
        {tasks.map((task) => (
          <SplitPane key={task.id} task={task} theme={theme} onRemove={() => onRemove(task.id)} onError={onError} />
        ))}
      </section>
    </main>
  );
}

function SplitPane({ task, theme, onRemove, onError }: { task: Task; theme: ThemeMode; onRemove: () => void; onError: (error: string) => void }) {
  const [message, setMessage] = useState('');
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const fitAddon = useRef<FitAddon | null>(null);
  const lastLogs = useRef('');
  const hasSession = hasTmuxSession(task);
  const hasStructured = hasStructuredSession(task);
  const canMessage = !isDiscordTask(task) && hasSession;

  useEffect(() => {
    if (!terminalRef.current) return;
    terminal.current = new Terminal({
      convertEol: true,
      disableStdin: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 12,
      theme: terminalTheme(theme),
    });
    fitAddon.current = new FitAddon();
    terminal.current.loadAddon(fitAddon.current);
    terminal.current.open(terminalRef.current);
    fitAddon.current.fit();
    const observer = new ResizeObserver(() => fitAddon.current?.fit());
    observer.observe(terminalRef.current);
    return () => {
      observer.disconnect();
      terminal.current?.dispose();
      terminal.current = null;
      fitAddon.current = null;
    };
  }, []);

  useEffect(() => {
    if (!terminal.current) return;
    terminal.current.options.theme = terminalTheme(theme);
  }, [theme]);

  useEffect(() => {
    lastLogs.current = '';
    terminal.current?.clear();
    if (!hasSession && hasStructured) {
      terminal.current?.write(structuredSessionMessage(task));
      return;
    }
    if (!hasSession) {
      terminal.current?.write(`No active session for "${task.title}".\r\nRun the task before opening split view.\r\n`);
      return;
    }
    const unsubscribe = window.runtime?.EventsOn?.(`agx:logs:${task.id}`, (payload) => {
      const event = payload as LogEvent;
      if (event.error) {
        terminal.current?.clear();
        terminal.current?.write(event.error);
        return;
      }
      const data = event.data ?? '';
      if (event.reset) {
        terminal.current?.clear();
        lastLogs.current = data;
        terminal.current?.write(data.replace(/\n/g, '\r\n'));
        return;
      }
      lastLogs.current += data;
      terminal.current?.write(data.replace(/\n/g, '\r\n'));
    });
    void api.StartLogStream(task.id, 500).catch((err) => {
      terminal.current?.clear();
      terminal.current?.write(String(err));
    });
    return () => {
      unsubscribe?.();
      void api.StopLogStream(task.id).catch(() => {});
    };
  }, [task.id, task.title, task.agent, hasSession, hasStructured]);

  async function send() {
    const text = message.trim();
    if (!text) return;
    if (!canMessage) {
      onError('Task has no active session. Run it first.');
      return;
    }
    try {
      await api.SendMessage(task.id, text);
      await api.RecordTaskInput(task.id, text);
      setMessage('');
    } catch (err) {
      onError(String(err));
    }
  }

  return (
    <article className="split-pane">
      <header>
        <strong>{task.title}</strong>
        <button onClick={onRemove}>Close</button>
      </header>
      <div className="split-terminal" ref={terminalRef} />
      <div className="split-input">
        <input
          value={message}
          disabled={!canMessage}
          onChange={(event) => setMessage(event.target.value)}
          onKeyDown={(event) => {
            if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
              event.preventDefault();
              void send();
            }
          }}
          placeholder={canMessage ? 'Message agent' : 'Run task to start a session'}
        />
        <button disabled={!canMessage} onClick={send}>Send</button>
      </div>
    </article>
  );
}

function Header({ title, subtitle, detail, theme, onToggleTheme, children }: { title: string; subtitle: string; detail?: string; theme: ThemeMode; onToggleTheme: () => void; children?: React.ReactNode }) {
  const [isFullscreen, setIsFullscreen] = useState(false);

  useEffect(() => {
    let alive = true;
    void window.runtime?.WindowIsFullscreen?.().then((value) => {
      if (alive) setIsFullscreen(value);
    });
    return () => {
      alive = false;
    };
  }, []);

  const toggleFullscreen = useCallback(() => {
    if (isFullscreen) {
      window.runtime?.WindowUnfullscreen?.();
      setIsFullscreen(false);
      return;
    }
    window.runtime?.WindowFullscreen?.();
    setIsFullscreen(true);
  }, [isFullscreen]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'F11' || ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f')) {
        event.preventDefault();
        toggleFullscreen();
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [toggleFullscreen]);

  return (
    <header className="topbar">
      <div>
        <h1>{title}</h1>
        <p>{subtitle}</p>
        {detail && <p className="topbar-detail">{detail}</p>}
      </div>
      <div className="topbar-actions">
        <IconButton label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} onClick={onToggleTheme}>
          {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
        </IconButton>
        <IconButton label={isFullscreen ? 'Exit fullscreen' : 'Enter fullscreen'} onClick={toggleFullscreen}>
          {isFullscreen ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
        </IconButton>
        {children}
      </div>
    </header>
  );
}

function Segmented({ value, onChange }: { value: ViewMode; onChange: (mode: ViewMode) => void }) {
  return (
    <div className="segmented">
      <IconButton label="Grid view" active={value === 'grid'} onClick={() => onChange('grid')}>
        <Grid2X2 size={17} />
      </IconButton>
      <IconButton label="List view" active={value === 'list'} onClick={() => onChange('list')}>
        <List size={17} />
      </IconButton>
    </div>
  );
}

function IconButton({ label, active, disabled, onClick, children }: { label: string; active?: boolean; disabled?: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button className={`icon-button ${active ? 'active' : ''}`} title={label} aria-label={label} disabled={disabled} onClick={onClick}>
      {children}
    </button>
  );
}

function ErrorBar({ error }: { error: string }) {
  return error ? <div className="error-bar">{error}</div> : null;
}

function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="empty-state">
      <h2>{title}</h2>
      <p>{detail}</p>
    </section>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
