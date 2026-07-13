export type Project = {
  id: string;
  name: string;
  path: string;
  description?: string;
  defaultAgent?: string;
  accessGranted: boolean;
  accessError?: string;
  languages?: LanguageStat[];
  taskCount: number;
  activeCount: number;
  waitingCount: number;
  completeCount: number;
  offlineCount: number;
  lastOpened: string;
  createdAt: string;
};

export type LanguageStat = {
  name: string;
  files: number;
  percentage: number;
};

export type ProjectCandidate = {
  name: string;
  path: string;
  description?: string;
  languages?: LanguageStat[];
  isRegistered: boolean;
};

export type Task = {
  id: string;
  projectId: string;
  title: string;
  description?: string;
  lastUserPrompt?: string;
  interface: TaskInterface;
  workspaceMode: WorkspaceMode;
  status: TaskStatus;
  agent: string;
  allMighty: boolean;
  sessionName?: string;
  worktreePath?: string;
  branchName?: string;
  agentThreadId?: string;
  agentStreamKind?: string;
  discordSync?: TaskDiscordSync;
  createdAt: string;
  updatedAt: string;
};

export type TaskDiscordSync = {
  status: 'pending' | 'synced' | 'failed';
  attempts: number;
  discordChannelId?: string;
  lastSuccessAt?: string;
  lastFailureAt?: string;
  lastError?: string;
  updatedAt: string;
};

export type TaskTranscriptMessage = {
  id: number;
  taskId: string;
  turnId?: string;
  role: 'user' | 'assistant' | 'system' | 'status' | 'tool_trace';
  body: string;
  createdAt: string;
  updatedAt: string;
};

export type Agent = {
  name: string;
  command: string;
  description: string;
  available: boolean;
};

export type DiscordStatusInfo = {
  enabled: boolean;
  connected: boolean;
  guildId?: string;
  guildName?: string;
  allowedUserIds?: string[];
  maskedBotToken?: string;
  uptimeSeconds: number;
  error?: string;
  lockedBy?: string;
  sync: DiscordSyncJob;
};

export type DiscordSyncJob = {
  running: boolean;
  kind?: string;
  stage?: string;
  startedAt?: string;
  completedAt?: string;
  error?: string;
};

export type RuntimeStatusInfo = {
  running: boolean;
  pid?: number;
  version?: string;
  startedAt?: string;
  uptimeSeconds: number;
  configDir?: string;
  socketPath: string;
  lockPath: string;
  transport?: string;
  recovery: {
    offline?: number;
    cleared?: number;
    orphans?: number;
  };
  error?: string;
};

export type RuntimeConfigInfo = {
  defaultAgent: string;
  voiceStt: VoiceSTTConfig;
};

export type VoiceSTTConfig = {
  mode: 'disabled' | 'auto' | 'enabled';
  ffmpegPath: string;
  whisperPath: string;
  modelPath: string;
  language: string;
  timeout: string;
};

export type FileEntry = {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
};

export type DirectoryEntry = {
  name: string;
  path: string;
};

export type TaskStatus = 'active' | 'waiting' | 'complete' | 'offline';
export type TaskInterface = 'local' | 'discord';
export type WorkspaceMode = 'worktree' | 'project';
export type ViewMode = 'grid' | 'list';
