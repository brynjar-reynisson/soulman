// web/src/components/icons.tsx
export function DownloadIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <path d="M10 3v9m0 0-3.5-3.5M10 12l3.5-3.5M4 15.5h12" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function ShareIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <circle cx="15" cy="4.5" r="2" />
      <circle cx="5" cy="10" r="2" />
      <circle cx="15" cy="15.5" r="2" />
      <path d="M6.7 9 13.3 5.5M6.7 11 13.3 14.5" strokeLinecap="round" />
    </svg>
  );
}
