import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { TaskInterfaceTabs } from './TaskInterfaceTabs';

describe('TaskInterfaceTabs', () => {
  it('renders counts and forwards tab changes', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<TaskInterfaceTabs value="all" counts={{ all: 3, desktop: 2, discord: 1 }} onChange={onChange} />);

    expect(screen.getByRole('button', { name: /All/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('3')).not.toBeNull();
    expect(screen.getByText('2')).not.toBeNull();
    expect(screen.getByText('1')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: /Discord/ }));

    expect(onChange).toHaveBeenCalledWith('discord');
  });
});
