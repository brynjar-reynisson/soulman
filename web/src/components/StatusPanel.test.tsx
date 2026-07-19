import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusPanel } from './StatusPanel';

describe('StatusPanel', () => {
  it('renders each service and its up/down state', () => {
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up', 'action-svc': 'down' }} />);
    expect(screen.getByText('memory-svc')).toBeInTheDocument();
    expect(screen.getByText('up')).toBeInTheDocument();
    expect(screen.getByText('action-svc')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows a placeholder when there is no status data', () => {
    render(<StatusPanel initialStatus={null} />);
    expect(screen.getByText(/no status data/i)).toBeInTheDocument();
  });
});
