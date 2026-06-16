import React from 'react';
import {
  CheckCircle2,
  Code2,
  FolderOpen,
  GitBranch,
  List,
  LockKeyhole,
  MessageCircle,
  ShieldCheck,
  SquareTerminal,
} from 'lucide-react';
import type { Agent, Project, WorkspaceMode } from '../../types';
import { quickTaskTemplates, type QuickTaskTemplate } from '../../appLogic';

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
  onQuickTemplate: (template: QuickTaskTemplate) => void;
  onGrantAccess: () => void;
}) {
  return (
    <>
      <section className="task-toolbar">
        <input ref={titleInputRef} value={title} onChange={(event) => onTitleChange(event.target.value)} placeholder="Task title" />
        <input value={description} onChange={(event) => onDescriptionChange(event.target.value)} placeholder="Prompt or details" />
        <select value={agent} onChange={(event) => onAgentChange(event.target.value)} aria-label="Agent">
          <option value="">Default agent</option>
          {agents.map((item) => (
            <option key={item.name} value={item.name}>
              {item.name}{item.available ? '' : ' (missing)'}
            </option>
          ))}
        </select>
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
