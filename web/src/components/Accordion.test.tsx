import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Accordion } from './Accordion';

const items = [
  { id: 'a', label: 'Alpha', badge: '2', content: <p>alpha body</p> },
  { id: 'b', label: 'Beta', content: <p>beta body</p> },
];

describe('Accordion', () => {
  it("renders only the open item's content", () => {
    render(<Accordion items={items} openId="a" onToggle={vi.fn()} />);
    expect(screen.getByText('alpha body')).toBeInTheDocument();
    expect(screen.queryByText('beta body')).not.toBeInTheDocument();
  });

  it('clicking a closed header calls onToggle(id)', async () => {
    const onToggle = vi.fn();
    render(<Accordion items={items} openId="a" onToggle={onToggle} />);
    await userEvent.click(screen.getByRole('button', { name: /Beta/ }));
    expect(onToggle).toHaveBeenCalledWith('b');
  });

  it('clicking the open header calls onToggle(null)', async () => {
    const onToggle = vi.fn();
    render(<Accordion items={items} openId="a" onToggle={onToggle} />);
    await userEvent.click(screen.getByRole('button', { name: /Alpha/ }));
    expect(onToggle).toHaveBeenCalledWith(null);
  });

  it('sets aria-expanded', () => {
    render(<Accordion items={items} openId="a" onToggle={vi.fn()} />);
    expect(screen.getByRole('button', { name: /Alpha/ })).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: /Beta/ })).toHaveAttribute('aria-expanded', 'false');
  });
});
