// web/src/components/icons.test.tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { DownloadIcon, ShareIcon } from './icons';

describe('icons', () => {
  it('DownloadIcon renders an svg element', () => {
    const { container } = render(<DownloadIcon />);
    expect(container.querySelector('svg')).toBeInTheDocument();
  });

  it('ShareIcon renders an svg element', () => {
    const { container } = render(<ShareIcon />);
    expect(container.querySelector('svg')).toBeInTheDocument();
  });

  it('forwards extra props (e.g. a test id) onto the svg element', () => {
    const { container } = render(<DownloadIcon data-testid="dl-icon" />);
    expect(container.querySelector('[data-testid="dl-icon"]')).toBeInTheDocument();
  });
});
