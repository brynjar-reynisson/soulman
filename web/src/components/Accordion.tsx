import type { ReactNode } from 'react';

export interface AccordionItem {
  id: string;
  label: string;
  badge?: string;
  content: ReactNode;
}

// Controlled accordion: the parent owns which item is open, so it can mirror
// that into the URL and reset child accordions when a parent changes.
export function Accordion({
  items,
  openId,
  onToggle,
}: {
  items: AccordionItem[];
  openId: string | null;
  onToggle: (id: string | null) => void;
}) {
  return (
    <div className="divide-y divide-gray-200 rounded border border-gray-200 bg-white">
      {items.map((item) => {
        const open = item.id === openId;
        return (
          <div key={item.id}>
            <button
              type="button"
              aria-expanded={open}
              onClick={() => onToggle(open ? null : item.id)}
              className="flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium hover:bg-gray-50"
            >
              <span>
                {open ? '▾' : '▸'} {item.label}
              </span>
              {item.badge !== undefined && <span className="text-xs text-gray-500">{item.badge}</span>}
            </button>
            {open && <div className="px-3 pb-3">{item.content}</div>}
          </div>
        );
      })}
    </div>
  );
}
