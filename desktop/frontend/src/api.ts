import type { Agent, DirectoryEntry, DiscordStatusInfo, FileEntry, Project, ProjectCandidate, RuntimeStatusInfo, Task, TaskTranscriptMessage, WorkspaceMode } from './types';

export type MonitorTask = Task & {
  projectName: string;
  projectPath: string;
};

type WailsApp = {
  ListProjects(): Promise<Project[]>;
  SelectProjectDirectory(defaultDirectory: string): Promise<string>;
  ValidateProjectDirectory(path: string): Promise<ProjectCandidate>;
  GrantProjectAccess(projectID: string): Promise<Project>;
  ListProjectCandidates(limit: number): Promise<ProjectCandidate[]>;
  HomeDirectory(): Promise<string>;
  ListDirectories(path: string): Promise<DirectoryEntry[]>;
  RegisterProject(path: string, name: string, description: string): Promise<Project>;
  UpdateProject(projectID: string, name: string, description: string): Promise<Project>;
  GetProject(id: string): Promise<Project>;
  DeleteProject(projectID: string): Promise<void>;
  ResetDatabase(): Promise<void>;
  RuntimeStatus(): Promise<RuntimeStatusInfo>;
  RuntimeStart(): Promise<RuntimeStatusInfo>;
  RuntimeInstallService(): Promise<RuntimeStatusInfo>;
  RuntimeStop(): Promise<RuntimeStatusInfo>;
  DiscordStatus(): Promise<DiscordStatusInfo>;
  DiscordConnect(token: string, guildID: string, allowedUserID: string): Promise<DiscordStatusInfo>;
  DiscordSync(): Promise<DiscordStatusInfo>;
  DiscordSoftSync(): Promise<DiscordStatusInfo>;
  DiscordHardSync(): Promise<DiscordStatusInfo>;
  DiscordResetManagedChannels(): Promise<DiscordStatusInfo>;
  DiscordDisconnect(): Promise<DiscordStatusInfo>;
  OpenDiscordInvite(token: string): Promise<void>;
  ListTasks(projectID: string): Promise<Task[]>;
  ListTaskTranscript(taskID: string, limit: number): Promise<TaskTranscriptMessage[]>;
  ListMonitorTasks(): Promise<MonitorTask[]>;
  CreateTask(projectID: string, title: string, description: string, agent: string, allMighty: boolean, workspaceMode: WorkspaceMode): Promise<Task>;
  CreateTaskNoPrompt(projectID: string, title: string, agent: string, allMighty: boolean, workspaceMode: WorkspaceMode): Promise<Task>;
  CreateDiscordTask(projectID: string, title: string, description: string, agent: string, allMighty: boolean, workspaceMode: WorkspaceMode): Promise<Task>;
  UpdateTaskTitle(taskID: string, title: string): Promise<Task>;
  RunTask(taskID: string): Promise<void>;
  StopTask(taskID: string): Promise<void>;
  DeleteTask(taskID: string): Promise<void>;
  SendMessage(taskID: string, message: string): Promise<void>;
  RecordTaskInput(taskID: string, message: string): Promise<void>;
  SendInput(taskID: string, data: string): Promise<void>;
  ResizeTaskTerminal(taskID: string, cols: number, rows: number): Promise<void>;
  GetLogs(taskID: string, lines: number): Promise<string>;
  StartLogStream(taskID: string, lines: number): Promise<void>;
  StopLogStream(taskID: string): Promise<void>;
  GetTaskStatus(taskID: string): Promise<string>;
  ListAvailableAgents(projectID: string): Promise<Agent[]>;
  ListDirectory(projectID: string, relativePath: string, showHidden: boolean): Promise<FileEntry[]>;
  ListTaskDirectory(taskID: string, relativePath: string, showHidden: boolean): Promise<FileEntry[]>;
  ReadFile(projectID: string, relativePath: string): Promise<string>;
  ReadTaskFile(taskID: string, relativePath: string): Promise<string>;
  SearchFiles(projectID: string, query: string, limit: number): Promise<string[]>;
  SearchTaskFiles(taskID: string, query: string, limit: number): Promise<string[]>;
  ComposePrompt(message: string, contextPaths: string[]): Promise<string>;
  ComposePromptWithFiles(projectID: string, message: string, contextPaths: string[], includeContents: boolean): Promise<string>;
  ComposeTaskPromptWithFiles(taskID: string, message: string, contextPaths: string[], includeContents: boolean): Promise<string>;
};

declare global {
  interface Window {
    go?: {
      desktop?: {
        App?: WailsApp;
      };
    };
    runtime?: {
      WindowFullscreen?: () => void;
      WindowUnfullscreen?: () => void;
      WindowIsFullscreen?: () => Promise<boolean>;
      EventsOn?: (eventName: string, callback: (...data: unknown[]) => void) => () => void;
    };
  }
}

export type LogEvent = {
  taskId: string;
  data?: string;
  reset: boolean;
  error?: string;
};

export type MetadataEvent = {
  projectId?: string;
};

const demoProjects: Project[] = [
  {
    id: 'demo',
    name: 'No Wails Runtime',
    path: 'Start this through agx-desktop to load real projects.',
    accessGranted: false,
    taskCount: 0,
    activeCount: 0,
    waitingCount: 0,
    completeCount: 0,
    offlineCount: 0,
    lastOpened: new Date().toISOString(),
    createdAt: new Date().toISOString(),
  },
];

function app(): WailsApp | undefined {
  return window.go?.desktop?.App;
}

const activeLogStreams = new Map<string, number>();

async function startSharedLogStream(taskID: string, lines: number): Promise<void> {
  const current = activeLogStreams.get(taskID) ?? 0;
  activeLogStreams.set(taskID, current + 1);
  try {
    await app()?.StartLogStream(taskID, lines);
  } catch (err) {
    const latest = activeLogStreams.get(taskID) ?? 0;
    if (latest <= 1) {
      activeLogStreams.delete(taskID);
    } else {
      activeLogStreams.set(taskID, latest - 1);
    }
    throw err;
  }
}

async function stopSharedLogStream(taskID: string): Promise<void> {
  const current = activeLogStreams.get(taskID) ?? 0;
  if (current <= 0) return;
  if (current > 1) {
    activeLogStreams.set(taskID, current - 1);
    return;
  }
  activeLogStreams.delete(taskID);
  await app()?.StopLogStream(taskID);
}

export const api: WailsApp = {
  async ListProjects() {
    return app()?.ListProjects() ?? demoProjects;
  },
  async SelectProjectDirectory(defaultDirectory) {
    return app()?.SelectProjectDirectory(defaultDirectory) ?? '';
  },
  async ValidateProjectDirectory(path) {
    const candidate = await app()?.ValidateProjectDirectory(path);
    if (!candidate) throw new Error('Wails runtime is not connected');
    return candidate;
  },
  async GrantProjectAccess(projectID) {
    const project = await app()?.GrantProjectAccess(projectID);
    if (!project) throw new Error('Wails runtime is not connected');
    return project;
  },
  async ListProjectCandidates(limit) {
    return app()?.ListProjectCandidates(limit) ?? [];
  },
  async HomeDirectory() {
    return app()?.HomeDirectory() ?? '';
  },
  async ListDirectories(path) {
    return app()?.ListDirectories(path) ?? [];
  },
  async RegisterProject(path, name, description) {
    const project = await app()?.RegisterProject(path, name, description);
    if (!project) throw new Error('Wails runtime is not connected');
    return project;
  },
  async UpdateProject(projectID, name, description) {
    const project = await app()?.UpdateProject(projectID, name, description);
    if (!project) throw new Error('Wails runtime is not connected');
    return project;
  },
  async GetProject(id) {
    const project = await app()?.GetProject(id);
    if (!project) throw new Error('Wails runtime is not connected');
    return project;
  },
  async DeleteProject(projectID) {
    await app()?.DeleteProject(projectID);
  },
  async ResetDatabase() {
    await app()?.ResetDatabase();
  },
  async RuntimeStatus() {
    return app()?.RuntimeStatus() ?? {
      running: false,
      uptimeSeconds: 0,
      socketPath: '',
      lockPath: '',
      recovery: {},
      error: 'Wails runtime is not connected',
    };
  },
  async RuntimeStart() {
    const status = await app()?.RuntimeStart();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async RuntimeInstallService() {
    const status = await app()?.RuntimeInstallService();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async RuntimeStop() {
    const status = await app()?.RuntimeStop();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordStatus() {
    return app()?.DiscordStatus() ?? { enabled: false, connected: false, uptimeSeconds: 0, sync: { running: false } };
  },
  async DiscordConnect(token, guildID, allowedUserID) {
    const status = await app()?.DiscordConnect(token, guildID, allowedUserID.trim());
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordSync() {
    const status = await app()?.DiscordSoftSync?.() ?? await app()?.DiscordSync();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordSoftSync() {
    const status = await app()?.DiscordSoftSync?.() ?? await app()?.DiscordSync();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordHardSync() {
    const status = await app()?.DiscordHardSync?.() ?? await app()?.DiscordResetManagedChannels();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordResetManagedChannels() {
    const status = await app()?.DiscordHardSync?.() ?? await app()?.DiscordResetManagedChannels();
    if (!status) throw new Error('Wails runtime is not connected');
    return status;
  },
  async DiscordDisconnect() {
    return app()?.DiscordDisconnect() ?? { enabled: false, connected: false, uptimeSeconds: 0, sync: { running: false } };
  },
  async OpenDiscordInvite(token) {
    const instance = app();
    if (!instance?.OpenDiscordInvite) throw new Error('Wails runtime is not connected');
    await instance.OpenDiscordInvite(token);
  },
  async ListTasks(projectID) {
    return app()?.ListTasks(projectID) ?? [];
  },
  async ListTaskTranscript(taskID, limit) {
    return app()?.ListTaskTranscript(taskID, limit) ?? [];
  },
  async ListMonitorTasks() {
    return app()?.ListMonitorTasks() ?? [];
  },
  async CreateTask(projectID, title, description, agent, allMighty, workspaceMode) {
    const task = await app()?.CreateTask(projectID, title, description, agent, allMighty, workspaceMode);
    if (!task) throw new Error('Wails runtime is not connected');
    return task;
  },
  async CreateTaskNoPrompt(projectID, title, agent, allMighty, workspaceMode) {
    const task = await app()?.CreateTaskNoPrompt(projectID, title, agent, allMighty, workspaceMode);
    if (!task) throw new Error('Wails runtime is not connected');
    return task;
  },
  async CreateDiscordTask(projectID, title, description, agent, allMighty, workspaceMode) {
    const task = await app()?.CreateDiscordTask(projectID, title, description, agent, allMighty, workspaceMode);
    if (!task) throw new Error('Wails runtime is not connected');
    return task;
  },
  async UpdateTaskTitle(taskID, title) {
    const task = await app()?.UpdateTaskTitle(taskID, title);
    if (!task) throw new Error('Wails runtime is not connected');
    return task;
  },
  async RunTask(taskID) {
    await app()?.RunTask(taskID);
  },
  async StopTask(taskID) {
    await app()?.StopTask(taskID);
  },
  async DeleteTask(taskID) {
    await app()?.DeleteTask(taskID);
  },
  async SendMessage(taskID, message) {
    await app()?.SendMessage(taskID, message);
  },
  async RecordTaskInput(taskID, message) {
    await app()?.RecordTaskInput(taskID, message);
  },
  async SendInput(taskID, data) {
    await app()?.SendInput(taskID, data);
  },
  async ResizeTaskTerminal(taskID, cols, rows) {
    await app()?.ResizeTaskTerminal(taskID, cols, rows);
  },
  async GetLogs(taskID, lines) {
    return app()?.GetLogs(taskID, lines) ?? '';
  },
  async StartLogStream(taskID, lines) {
    await startSharedLogStream(taskID, lines);
  },
  async StopLogStream(taskID) {
    await stopSharedLogStream(taskID);
  },
  async GetTaskStatus(taskID) {
    return app()?.GetTaskStatus(taskID) ?? 'offline';
  },
  async ListAvailableAgents(projectID) {
    return app()?.ListAvailableAgents(projectID) ?? [];
  },
  async ListDirectory(projectID, relativePath, showHidden) {
    return app()?.ListDirectory(projectID, relativePath, showHidden) ?? [];
  },
  async ListTaskDirectory(taskID, relativePath, showHidden) {
    return app()?.ListTaskDirectory(taskID, relativePath, showHidden) ?? [];
  },
  async ReadFile(projectID, relativePath) {
    return app()?.ReadFile(projectID, relativePath) ?? '';
  },
  async ReadTaskFile(taskID, relativePath) {
    return app()?.ReadTaskFile(taskID, relativePath) ?? '';
  },
  async SearchFiles(projectID, query, limit) {
    return app()?.SearchFiles(projectID, query, limit) ?? [];
  },
  async SearchTaskFiles(taskID, query, limit) {
    return app()?.SearchTaskFiles(taskID, query, limit) ?? [];
  },
  async ComposePrompt(message, contextPaths) {
    const bound = app();
    if (bound) return bound.ComposePrompt(message, contextPaths);
    const unique = Array.from(new Set(contextPaths.filter(Boolean)));
    return unique.length === 0
      ? message
      : `Read these files first and use them as context: ${unique.join(', ')}\n${message}`;
  },
  async ComposePromptWithFiles(projectID, message, contextPaths, includeContents) {
    const bound = app();
    if (bound) return bound.ComposePromptWithFiles(projectID, message, contextPaths, includeContents);
    return api.ComposePrompt(message, contextPaths);
  },
  async ComposeTaskPromptWithFiles(taskID, message, contextPaths, includeContents) {
    const bound = app();
    if (bound) return bound.ComposeTaskPromptWithFiles(taskID, message, contextPaths, includeContents);
    return api.ComposePrompt(message, contextPaths);
  },
};
