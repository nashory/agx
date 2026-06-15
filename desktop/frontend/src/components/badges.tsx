import { FolderOpen, GitBranch, MessageCircle, ShieldCheck } from 'lucide-react';
import type { WorkspaceMode } from '../types';
import { agentLabel } from '../appLogic';

export function AllMightyBadge() {
  return (
    <span className="all-mighty-badge" title="Runs without approval prompts or sandbox restrictions where supported">
      <ShieldCheck size={13} />
      All-mighty
    </span>
  );
}

export function AgentBadge({ agent }: { agent: string }) {
  return (
    <span className={`agent-badge agent-${agent.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`} title={agent || 'Default agent'}>
      {agentLabel(agent)}
    </span>
  );
}

export function DiscordBadge() {
  return (
    <span className="discord-task-badge" title="Controlled from Discord">
      <MessageCircle size={13} />
      Discord
    </span>
  );
}

export function WorkspaceBadge({ mode }: { mode?: WorkspaceMode }) {
  const projectMode = mode === 'project';
  return (
    <span className={`workspace-task-badge ${projectMode ? 'project' : 'worktree'}`} title={projectMode ? 'Runs in the current project checkout' : 'Runs in an isolated task worktree'}>
      {projectMode ? <FolderOpen size={13} /> : <GitBranch size={13} />}
      {projectMode ? 'Project' : 'Worktree'}
    </span>
  );
}
