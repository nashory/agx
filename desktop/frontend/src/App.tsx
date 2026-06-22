import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  ArrowLeft,
  CheckSquare,
  ExternalLink,
  Folder,
  Keyboard,
  MessageCircle,
  Minus,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Plus,
  RefreshCw,
  Send,
  Settings as SettingsIcon,
  SquareTerminal,
  Trash2,
  X,
} from 'lucide-react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { api, type LogEvent, type MetadataEvent, type MonitorTask } from './api';
import { ActionLogConsole } from './actionLog';
import { CodePreview, isMarkdownPreviewPath } from './codePreview';
import { DiscordBadge } from './components/badges';
import { FilePanel } from './filePanel';
import { DiscordTaskDetail } from './features/discord/DiscordTaskDetail';
import { DiscordView } from './features/discord/DiscordView';
import { ActionErrorModal } from './features/errors/ActionErrorModal';
import { MonitorView } from './features/monitor/MonitorView';
import { GrantAccessModal } from './features/projects/GrantAccessModal';
import { ProjectView } from './features/projects/ProjectView';
import { RuntimeStartupView, SettingsView } from './features/settings/SettingsView';
import { ShortcutsView } from './features/shortcuts/ShortcutsView';
import { TaskCard, TaskList } from './features/tasks/TaskBoardItems';
import { QuickTaskModal } from './features/tasks/QuickTaskModal';
import { TaskCreateToolbar } from './features/tasks/TaskCreateToolbar';
import { TaskInterfaceTabs } from './features/tasks/TaskInterfaceTabs';
import { addUniquePaths, appendPromptPaths, pathsFromDrop } from './pathDrag';
import type { Agent, DiscordStatusInfo, Project, RuntimeConfigInfo, RuntimeStatusInfo, Task, TaskStatus, ViewMode, WorkspaceMode } from './types';
import { EmptyState, ErrorBar, Header, IconButton, Segmented, type ThemeMode } from './ui';
import {
  agentLabel,
  clampZoomLevel,
  defaultZoomLevel,
  errorMessage,
  focusMainContent,
  focusSidebarNavigation,
  hasStructuredSession,
  hasTmuxSession,
  isAgentContextClearCommand,
  isDiscordTask,
  isTaskStatus,
  isTerminalInput,
  isTextEntry,
  loadPreferences,
  loadZoomLevel,
  mainTabs,
  preferenceKey,
  projectGridColumns,
  relativeTime,
  sortTasks,
  statusClass,
  statusLabel,
  statusRank,
  structuredSessionMessage,
  taskInterfaceCounts,
  taskInterfaceLabel,
  taskPreviewDescription,
  tasksForInterfaceFilter,
  terminalTheme,
  timestamp,
  zoomPreferenceKey,
  zoomStep,
  type DesktopAction,
  type DesktopActionResult,
  type MainTab,
  type QuickTaskTemplate,
  type TaskInterfaceFilter,
  type UserPreferences,
} from './appLogic';
import './styles.css';

export default function App() {
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
      {actionError && <ActionErrorModal title={actionError.title} message={actionError.message} onClose={() => setActionError(null)} />}
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

function splitCSV(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
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
  const [selectedTaskIDs, setSelectedTaskIDs] = useState<Set<string>>(() => new Set());
  const [confirmingBulkDelete, setConfirmingBulkDelete] = useState(false);
  const [grantingAccess, setGrantingAccess] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const taskCounts = useMemo(() => taskInterfaceCounts(tasks), [tasks]);
  const visibleTasks = useMemo(() => tasksForInterfaceFilter(tasks, taskFilter), [tasks, taskFilter]);
  const focusedTask = visibleTasks.find((task) => task.id === focusedTaskID) ?? null;
  const selectedTasks = useMemo(() => visibleTasks.filter((task) => selectedTaskIDs.has(task.id)), [selectedTaskIDs, visibleTasks]);
  const allVisibleTasksSelected = visibleTasks.length > 0 && selectedTasks.length === visibleTasks.length;

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
    const visibleIDs = new Set(visibleTasks.map((task) => task.id));
    setSelectedTaskIDs((current) => {
      const next = new Set([...current].filter((taskID) => visibleIDs.has(taskID)));
      return next.size === current.size ? current : next;
    });
  }, [visibleTasks]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.altKey && event.key === 'Backspace') {
        event.preventDefault();
        onBack();
        return;
      }
      if (event.metaKey && !event.ctrlKey && !event.altKey && event.key.toLowerCase() === 't') {
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
        if (event.key === ' ' && focusedTaskID) {
          event.preventDefault();
          toggleTaskSelected(focusedTaskID);
          return;
        }
      }
      if (!(event.ctrlKey || event.metaKey)) return;
      switch (event.key) {
        case 'a':
          if (!isTextEntry(event.target) && visibleTasks.length > 0) {
            event.preventDefault();
            setSelectedTaskIDs(new Set(visibleTasks.map((task) => task.id)));
          }
          break;
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

  function toggleTaskSelected(taskID: string) {
    setSelectedTaskIDs((current) => {
      const next = new Set(current);
      if (next.has(taskID)) {
        next.delete(taskID);
      } else {
        next.add(taskID);
      }
      return next;
    });
  }

  function toggleAllVisibleTasks() {
    setSelectedTaskIDs(allVisibleTasksSelected ? new Set() : new Set(visibleTasks.map((task) => task.id)));
  }

  async function deleteSelectedTasks() {
    const targets = selectedTasks;
    if (targets.length === 0) return;
    setConfirmingBulkDelete(false);
    const failed = new Set<string>();
    const ok = await onAction(async () => {
      const failures: string[] = [];
      for (const task of targets) {
        try {
          await api.DeleteTask(task.id);
        } catch (err) {
          failed.add(task.id);
          failures.push(`${task.title}: ${errorMessage(err)}`);
        }
      }
      if (failures.length > 0) {
        throw new Error(`Failed to delete ${failures.length} of ${targets.length} tasks:\n${failures.join('\n')}`);
      }
    }, `delete ${targets.length} tasks`);
    setSelectedTaskIDs(ok ? new Set() : failed);
  }

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
      <TaskCreateToolbar
        project={project}
        title={title}
        description={description}
        agent={agent}
        agents={agents}
        allMighty={allMighty}
        workspaceMode={workspaceMode}
        attachToDiscord={attachToDiscord}
        discordConnected={discordConnected}
        busy={busy}
        grantingAccess={grantingAccess}
        titleInputRef={titleRef}
        onTitleChange={setTitle}
        onDescriptionChange={setDescription}
        onAgentChange={setAgent}
        onAllMightyChange={setAllMighty}
        onWorkspaceModeChange={setWorkspaceMode}
        onAttachToDiscordChange={setAttachToDiscord}
        onCreate={createTask}
        onQuickTemplate={setQuickTemplate}
        onGrantAccess={() => void grantAccess()}
      />
      <TaskInterfaceTabs value={taskFilter} counts={taskCounts} onChange={setTaskFilter} />
      {visibleTasks.length > 0 && (
        <section className={`task-bulk-toolbar ${selectedTasks.length > 0 ? 'active' : ''}`} aria-label="Task selection actions">
          <div className="task-bulk-summary">
            <IconButton label={allVisibleTasksSelected ? 'Clear task selection' : 'Select all visible tasks'} disabled={busy} onClick={toggleAllVisibleTasks}>
              {allVisibleTasksSelected ? <X size={17} /> : <CheckSquare size={17} />}
            </IconButton>
            <span>{selectedTasks.length === 0 ? 'No tasks selected' : `${selectedTasks.length} selected`}</span>
          </div>
          <button className="danger-button task-bulk-delete" disabled={busy || selectedTasks.length === 0} onClick={() => setConfirmingBulkDelete(true)}>
            <Trash2 size={16} />
            Delete
          </button>
        </section>
      )}
      <section className={`task-board-layout ${showTaskOutput ? 'with-output' : ''}`}>
        <section className="task-board-main">
          {visibleTasks.length === 0 ? (
            <EmptyState title={tasks.length === 0 ? 'No tasks' : `No ${taskInterfaceLabel(taskFilter)} tasks`} detail={tasks.length === 0 ? 'Create a task to start an agent session.' : 'Switch tabs or create a matching task.'} />
          ) : viewMode === 'list' ? (
            <TaskList tasks={visibleTasks} busy={busy} focusedTaskID={focusedTaskID} selectedTaskIDs={selectedTaskIDs} onFocusTask={setFocusedTaskID} onSelectTask={onSelectTask} onAction={onAction} onToggleSelect={toggleTaskSelected} />
          ) : (
            <section className="task-grid">
              {visibleTasks.map((task, index) => (
                <TaskCard key={task.id} task={task} busy={busy} focused={task.id === focusedTaskID} selected={selectedTaskIDs.has(task.id)} onFocus={() => setFocusedTaskID(task.id)} onOpen={() => onSelectTask(task)} onAction={onAction} onToggleSelect={() => toggleTaskSelected(task.id)} index={index} />
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
      {confirmingBulkDelete && (
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingBulkDelete(false)}>
          <section className="confirm-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Delete Tasks</h2>
            <p>Delete {selectedTasks.length} selected tasks from AGX? Each task will be stopped and its task worktree will be removed through the normal task cleanup path.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingBulkDelete(false)}>Cancel</button>
              <button className="danger-button" disabled={busy || selectedTasks.length === 0} onClick={() => void deleteSelectedTasks()}>Delete</button>
            </div>
          </section>
        </div>
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
