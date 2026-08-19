import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { Select } from './Select';

const options = [
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'muse', label: 'Muse Code' },
];

describe('Select', () => {
  it('opens a themed listbox and selects an option', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Select ariaLabel="Agent" value="claude" options={options} onChange={onChange} />);

    await user.click(screen.getByRole('combobox', { name: 'Agent' }));

    expect(screen.getByRole('listbox', { name: 'Agent' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Claude Code' })).toHaveAttribute('aria-selected', 'true');

    await user.click(screen.getByRole('option', { name: 'Muse Code' }));

    expect(onChange).toHaveBeenCalledWith('muse');
    expect(screen.queryByRole('listbox', { name: 'Agent' })).not.toBeInTheDocument();
  });

  it('supports keyboard navigation and escape', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Select ariaLabel="Agent" value="claude" options={options} onChange={onChange} />);
    const trigger = screen.getByRole('combobox', { name: 'Agent' });

    trigger.focus();
    await user.keyboard('{ArrowDown}{ArrowDown}{Enter}');

    expect(onChange).toHaveBeenCalledWith('codex');

    await user.keyboard('{Enter}{Escape}');
    expect(screen.queryByRole('listbox', { name: 'Agent' })).not.toBeInTheDocument();
  });

  it('closes when focus moves to an outside click target', async () => {
    const user = userEvent.setup();
    render(
      <>
        <Select ariaLabel="Agent" value="claude" options={options} onChange={vi.fn()} />
        <button type="button">Outside</button>
      </>,
    );

    await user.click(screen.getByRole('combobox', { name: 'Agent' }));
    await user.click(screen.getByRole('button', { name: 'Outside' }));

    expect(screen.queryByRole('listbox', { name: 'Agent' })).not.toBeInTheDocument();
  });
});
