import { Header, type ThemeMode } from '../../ui';

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

export function ShortcutsView({ theme, onToggleTheme }: { theme: ThemeMode; onToggleTheme: () => void }) {
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
