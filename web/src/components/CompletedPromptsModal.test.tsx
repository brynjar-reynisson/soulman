import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { CompletedPromptsModal } from './CompletedPromptsModal';
import type { Prompt } from '../api';

function makePrompt(overrides: Partial<Prompt>): Prompt {
  return {
    id: 1,
    project_name: 'proj',
    task_name: 'task',
    prompt_text: 'do the thing',
    state: 'DONE',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('CompletedPromptsModal', () => {
  it('shows the empty state when there are no DONE prompts', () => {
    render(
      <CompletedPromptsModal
        prompts={[makePrompt({ id: 1, state: 'NOT_STARTED' })]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText('No completed tasks yet.')).toBeInTheDocument();
  });

  it('renders project, task, and prompt text for a DONE prompt', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({
            id: 1,
            project_name: 'soulman',
            task_name: 'Add feature X',
            prompt_text: 'Implement feature X end to end.',
            state: 'DONE',
            done_at: '2026-09-01T00:00:00Z',
          }),
        ]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText('soulman')).toBeInTheDocument();
    expect(screen.getByText('Add feature X')).toBeInTheDocument();
    expect(screen.getByText('Implement feature X end to end.')).toBeInTheDocument();
  });

  it('excludes non-DONE prompts', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({ id: 1, task_name: 'not done', state: 'IMPLEMENTING' }),
          makePrompt({ id: 2, task_name: 'done one', state: 'DONE', done_at: '2026-09-01T00:00:00Z' }),
        ]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByText('not done')).not.toBeInTheDocument();
    expect(screen.getByText('done one')).toBeInTheDocument();
  });

  it('orders DONE prompts most-recently-completed first', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({ id: 1, task_name: 'older', state: 'DONE', done_at: '2026-09-01T00:00:00Z' }),
          makePrompt({ id: 2, task_name: 'newer', state: 'DONE', done_at: '2026-09-03T00:00:00Z' }),
        ]}
        onClose={vi.fn()}
      />,
    );
    const taskCells = screen.getAllByText(/older|newer/);
    expect(taskCells[0]).toHaveTextContent('newer');
    expect(taskCells[1]).toHaveTextContent('older');
  });

  it('caps the list at 20 entries even when more DONE prompts exist', () => {
    const prompts = Array.from({ length: 25 }, (_, i) =>
      makePrompt({
        id: i + 1,
        task_name: `task-${i}`,
        state: 'DONE',
        done_at: `2026-09-${String((i % 28) + 1).padStart(2, '0')}T00:00:00Z`,
      }),
    );
    render(<CompletedPromptsModal prompts={prompts} onClose={vi.fn()} />);
    expect(screen.getAllByText(/^task-\d+$/)).toHaveLength(20);
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(screen.getByLabelText('Close'));
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onClose when the backdrop is clicked', () => {
    const onClose = vi.fn();
    const { container } = render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(container.firstChild as Element);
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close when clicking inside the card', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(screen.getByText('Completed Tasks'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('calls onClose on Escape keydown', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });
});
