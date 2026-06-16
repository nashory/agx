import React from 'react';
import { Grid2X2, MessageCircle, SquareTerminal } from 'lucide-react';
import type { TaskInterfaceFilter } from '../../appLogic';

export function TaskInterfaceTabs({
  value,
  counts,
  onChange,
}: {
  value: TaskInterfaceFilter;
  counts: Record<TaskInterfaceFilter, number>;
  onChange: (value: TaskInterfaceFilter) => void;
}) {
  const tabs: Array<{ value: TaskInterfaceFilter; label: string; icon: React.ReactNode }> = [
    { value: 'all', label: 'All', icon: <Grid2X2 size={15} /> },
    { value: 'desktop', label: 'Desktop', icon: <SquareTerminal size={15} /> },
    { value: 'discord', label: 'Discord', icon: <MessageCircle size={15} /> },
  ];
  return (
    <nav className="task-interface-tabs" aria-label="Task type filter">
      {tabs.map((tab) => (
        <button key={tab.value} type="button" className={value === tab.value ? 'active' : ''} onClick={() => onChange(tab.value)} aria-pressed={value === tab.value}>
          {tab.icon}
          <span>{tab.label}</span>
          <strong>{counts[tab.value]}</strong>
        </button>
      ))}
    </nav>
  );
}
