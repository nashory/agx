import { useEffect, useState } from 'react';
import { FolderOpen, GitBranch, MessageCircle, X } from 'lucide-react';
import { AllMightyBadge, DiscordBadge, WorkspaceBadge } from '../../components/badges';
import type { Agent, WorkspaceMode } from '../../types';
import { IconButton } from '../../ui';
import { agentLabel, type QuickTaskTemplate } from '../../appLogic';

export function QuickTaskModal({
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
