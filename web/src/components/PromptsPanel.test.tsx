import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import type { Prompt } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetPrompts = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getPrompts: (...args: unknown[]) => mockGetPrompts(...args),
    createPrompt: vi.fn(),
    updatePromptState: vi.fn(),
  };
});

beforeEach(() => vi.clearAllMocks());

const activePrompt: Prompt = {
  id: 1,
  project_name: 'soulman',
  task_name: 'active task',
  prompt_text: 'work in progress',
  state: 'IMPLEMENTING',
  created_at: '2026-09-01T00:00:00Z',
};

const donePrompt: Prompt = {
  id: 2,
  project_name: 'soulman',
  task_name: 'finished task',
  prompt_text: 'already done',
  state: 'DONE',
  created_at: '2026-09-01T00:00:00Z',
  done_at: '2026-09-02T00:00:00Z',
};

describe('PromptsPanel', () => {
  it('renders the Completed link at the bottom of the box', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    expect(await screen.findByText('Completed')).toBeInTheDocument();
  });

  it('opens the CompletedPromptsModal showing a DONE prompt when Completed is clicked', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    await waitFor(() => expect(mockGetPrompts).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Completed'));

    expect(await screen.findByText('finished task')).toBeInTheDocument();
    expect(screen.getByText('Completed Tasks')).toBeInTheDocument();
  });

  it('closes the modal without disturbing the existing prompts table', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    await waitFor(() => expect(mockGetPrompts).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Completed'));
    await screen.findByText('Completed Tasks');
    fireEvent.click(screen.getByLabelText('Close'));

    expect(screen.queryByText('Completed Tasks')).not.toBeInTheDocument();
    expect(screen.getByText('active task')).toBeInTheDocument();
  });
});
