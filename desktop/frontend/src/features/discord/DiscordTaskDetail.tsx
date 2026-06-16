import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { ArrowDown, ArrowLeft, ArrowUp, PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { api } from '../../api';
import { CodePreview, isMarkdownPreviewPath, renderMarkdown } from '../../codePreview';
import { DiscordBadge } from '../../components/badges';
import { FilePanel } from '../../filePanel';
import type { Project, Task, TaskTranscriptMessage } from '../../types';
import { ErrorBar, Header, IconButton, type ThemeMode } from '../../ui';
import { agentLabel, discordSyncLabel, discordSyncTime, statusLabel } from '../../appLogic';
import { DiscordTaskSyncAction } from './DiscordTaskSyncAction';

export function DiscordTaskDetail({
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

  const syncStatusClass = task.discordSync?.status ?? 'not-started';
  const syncStatusTime = discordSyncTime(task.discordSync);

  return (
    <main className="session-shell discord-task-detail-shell">
      <Header
        title={`${project.name} / ${task.title}`}
        subtitle={`${agentLabel(task.agent)} · ${statusLabel(task.status)} · Discord`}
        detail={task.worktreePath ?? project.path}
        theme={theme}
        onToggleTheme={onToggleTheme}
      >
        <DiscordTaskSyncAction taskId={task.id} taskTitle={task.title} onError={onError} onLog={onLog} onChanged={onChanged} />
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
                <div className={`discord-sync-status ${syncStatusClass}`}>
                  <span>{discordSyncLabel(task.discordSync)}</span>
                  {task.discordSync?.attempts ? <span>{task.discordSync.attempts} attempt{task.discordSync.attempts === 1 ? '' : 's'}</span> : null}
                  {syncStatusTime && <span>{syncStatusTime}</span>}
                </div>
                {task.discordSync?.lastError && <p className="discord-sync-error">{task.discordSync.lastError}</p>}
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
