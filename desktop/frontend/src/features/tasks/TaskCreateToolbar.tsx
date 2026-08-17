import React, { useEffect, useRef, useState } from 'react';
import {
  CheckCircle2,
  ChevronDown,
  Code2,
  FolderOpen,
  GitBranch,
  List,
  LockKeyhole,
  MessageCircle,
  PlayCircle,
  ShieldCheck,
  SquareTerminal,
} from 'lucide-react';
import type { Agent, Project, WorkspaceMode } from '../../types';
import { agentLabel, quickTaskTemplates, type QuickTaskTemplate } from '../../appLogic';

export function TaskCreateToolbar({
  project,
  title,
  description,
  agent,
  agents,
  allMighty,
  workspaceMode,
  attachToDiscord,
  discordConnected,
  busy,
  grantingAccess,
  titleInputRef,
  onTitleChange,
  onDescriptionChange,
  onAgentChange,
  onAllMightyChange,
  onWorkspaceModeChange,
  onAttachToDiscordChange,
  onCreate,
  onMainQuickTask,
  onQuickTemplate,
  onGrantAccess,
}: {
  project: Project;
  title: string;
  description: string;
  agent: string;
  agents: Agent[];
  allMighty: boolean;
  workspaceMode: WorkspaceMode;
  attachToDiscord: boolean;
  discordConnected: boolean;
  busy: boolean;
  grantingAccess: boolean;
  titleInputRef: React.Ref<HTMLInputElement>;
  onTitleChange: (value: string) => void;
  onDescriptionChange: (value: string) => void;
  onAgentChange: (value: string) => void;
  onAllMightyChange: (value: boolean) => void;
  onWorkspaceModeChange: (value: WorkspaceMode) => void;
  onAttachToDiscordChange: (value: boolean) => void;
  onCreate: () => void;
  onMainQuickTask: () => void;
  onQuickTemplate: (template: QuickTaskTemplate) => void;
  onGrantAccess: () => void;
}) {
  const [agentMenuOpen, setAgentMenuOpen] = useState(false);
  const agentMenuRef = useRef<HTMLDivElement>(null);
  const selectedAgent = agents.find((item) => item.name === agent);
  const fallbackSelectedAgent = agent && !selectedAgent ? unavailableAgent(agent) : null;
  const agentOptions = fallbackSelectedAgent ? [fallbackSelectedAgent, ...agents] : agents;
  const selectedAgentLabel = selectedAgent ? agentOptionLabel(selectedAgent) : fallbackSelectedAgent ? agentOptionLabel(fallbackSelectedAgent) : 'Default agent';

  useEffect(() => {
    if (!agentMenuOpen) {
      return;
    }

    const handlePointerDown = (event: PointerEvent) => {
      if (!agentMenuRef.current?.contains(event.target as Node)) {
        setAgentMenuOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setAgentMenuOpen(false);
      }
    };

    window.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [agentMenuOpen]);

  const selectAgent = (value: string) => {
    onAgentChange(value);
    setAgentMenuOpen(false);
  };

  return (
    <>
      <section className="task-toolbar">
        <input ref={titleInputRef} value={title} onChange={(event) => onTitleChange(event.target.value)} placeholder="Task title" />
        <input value={description} onChange={(event) => onDescriptionChange(event.target.value)} placeholder="Prompt or details" />
        <div className="agent-picker" ref={agentMenuRef}>
          <button type="button" className={`agent-picker-trigger ${agentMenuOpen ? 'open' : ''}`} onClick={() => setAgentMenuOpen((open) => !open)} aria-label={`Agent: ${selectedAgentLabel}`} aria-haspopup="listbox" aria-expanded={agentMenuOpen}>
            <span>{selectedAgentLabel}</span>
            <ChevronDown size={16} />
          </button>
          {agentMenuOpen && (
            <div className="agent-picker-menu" role="listbox" aria-label="Agent">
              <button type="button" role="option" aria-selected={agent === ''} className={agent === '' ? 'selected' : ''} onClick={() => selectAgent('')}>
                <span>Default agent</span>
                {agent === '' && <CheckCircle2 size={15} />}
              </button>
              {agentOptions.map((item) => (
                <button key={item.name} type="button" role="option" aria-selected={agent === item.name} className={agent === item.name ? 'selected' : ''} onClick={() => selectAgent(item.name)}>
                  <span>{agentOptionLabel(item)}</span>
                  {agent === item.name && <CheckCircle2 size={15} />}
                </button>
              ))}
            </div>
          )}
        </div>
        <label className={`all-mighty-toggle ${allMighty ? 'active' : ''}`} title="Run without approval prompts or sandbox restrictions where the agent supports it">
          <input type="checkbox" checked={allMighty} onChange={(event) => onAllMightyChange(event.target.checked)} />
          <ShieldCheck size={16} />
          <span>All-mighty</span>
        </label>
        <div className="workspace-mode-toggle" role="group" aria-label="Workspace mode">
          <button type="button" className={workspaceMode === 'worktree' ? 'active' : ''} onClick={() => onWorkspaceModeChange('worktree')} title="Run in an isolated task worktree">
            <GitBranch size={15} />
            <span>Worktree</span>
          </button>
          <button type="button" className={workspaceMode === 'project' ? 'active' : ''} onClick={() => onWorkspaceModeChange('project')} title="Run in the current project checkout. Only one project-mode task can be active.">
            <FolderOpen size={15} />
            <span>Project</span>
          </button>
        </div>
        <label className={`discord-attach-toggle ${attachToDiscord ? 'active' : ''}`} title={discordConnected ? 'Create this task as Discord-controlled. Desktop input will be read-only.' : 'Connect Discord before creating Discord-controlled tasks.'}>
          <input type="checkbox" checked={attachToDiscord} onChange={(event) => onAttachToDiscordChange(event.target.checked)} />
          <MessageCircle size={16} />
          <span>Attach to Discord</span>
        </label>
        <button className="primary-button" onClick={onCreate} disabled={busy || !project.accessGranted || !title.trim() || (attachToDiscord && !discordConnected)}>
          Create
        </button>
      </section>
      <section className="quick-task-row" aria-label="Quick task templates">
        <div className="quick-task-buttons">
          <button className="quick-task-button main-quick-task-button" disabled={busy || !project.accessGranted || !discordConnected} onClick={onMainQuickTask}>
            <PlayCircle size={16} />
            <span>Main</span>
          </button>
          {quickTaskTemplates.map((template) => (
            <button className="quick-task-button" key={template.id} disabled={busy || !project.accessGranted || (attachToDiscord && !discordConnected)} onClick={() => onQuickTemplate(template)}>
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
          <button className="text-button project-access-button" onClick={onGrantAccess} disabled={grantingAccess} title={project.accessGranted ? 'Re-run project access repair and validation' : 'Grant project write access before creating tasks'}>
            {project.accessGranted && <CheckCircle2 size={15} />}
            {!project.accessGranted && <LockKeyhole size={15} />}
            {grantingAccess ? 'Granting...' : project.accessGranted ? 'Access Granted' : 'Grant Access'}
          </button>
        </div>
      </section>
    </>
  );
}

function agentOptionLabel(agent: Agent): string {
  return `${agentLabel(agent.name)}${agent.available ? '' : ' (missing)'}`;
}

function unavailableAgent(name: string): Agent {
  return { name, command: name, description: '', available: false };
}
