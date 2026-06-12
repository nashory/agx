import { useEffect, useRef, useState } from 'react';

export function ActionLogConsole({ logs }: { logs: string[] }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const bodyRef = useRef<HTMLPreElement>(null);
  const text = logs.length > 0 ? logs.join('\n') : 'No desktop actions yet.';

  useEffect(() => {
    if (open && bodyRef.current) {
      bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
    }
  }, [logs, open]);

  async function copyLogs() {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }

  return (
    <aside className={`action-log ${open ? 'open' : ''}`}>
      <button className="action-log-toggle" onClick={() => setOpen((value) => !value)}>
        AGX Logs
        {logs.length > 0 && <span>{logs.length}</span>}
      </button>
      {open && (
        <section className="action-log-panel">
          <header className="action-log-header">
            <strong>AGX Logs</strong>
            <button className="text-button" onClick={() => void copyLogs()}>{copied ? 'Copied' : 'Copy'}</button>
          </header>
          <pre ref={bodyRef} className="action-log-body">
            {text}
          </pre>
        </section>
      )}
    </aside>
  );
}
