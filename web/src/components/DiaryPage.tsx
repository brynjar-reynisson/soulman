import { useEffect, useState, type ReactNode } from 'react';
import { getAccessToken } from '../auth';
import {
  ApiError,
  createDiaryEntry,
  getDiaryEntries,
  getDiaryTree,
  saveDiaryEntry,
  type DiaryEntry,
  type DiaryYear,
} from '../api';
import { getParam, setParams } from '../urlState';
import { Accordion } from './Accordion';

const MONTH_NAMES = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

const UNREACHABLE = "Diary folder isn't reachable — is Google Drive running?";

function monthName(month: string): string {
  return MONTH_NAMES[parseInt(month, 10) - 1] ?? month;
}

function newestFirst<T>(arr: T[], key: (t: T) => string): T[] {
  return [...arr].sort((a, b) => key(b).localeCompare(key(a)));
}

function sum(counts: number[]): number {
  return counts.reduce((a, b) => a + b, 0);
}

function describeError(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.status === 503 ? UNREACHABLE : fallback;
}

export function DiaryPage({ onBack }: { onBack: () => void }) {
  const [years, setYears] = useState<DiaryYear[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [openYear, setOpenYear] = useState<string | null>(getParam('diaryYear'));
  const [openMonth, setOpenMonth] = useState<string | null>(getParam('diaryMonth'));
  const [openDay, setOpenDay] = useState<string | null>(getParam('diaryDay'));
  const [entries, setEntries] = useState<DiaryEntry[] | null>(null);
  const [entriesVersion, setEntriesVersion] = useState(0);
  const [newText, setNewText] = useState('');
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState('');

  async function refreshTree() {
    const token = await getAccessToken();
    try {
      const data = await getDiaryTree(token);
      setYears(data?.years ?? []);
      setError(null);
    } catch (err) {
      setError(describeError(err, 'Diary unavailable'));
    }
  }

  useEffect(() => {
    refreshTree();
  }, []);

  // Only honour URL-restored selections that actually exist in the tree.
  const yearNode = years?.find((y) => y.year === openYear) ?? null;
  const monthNode = yearNode?.months.find((m) => m.month === openMonth) ?? null;
  const dayNode = monthNode?.days.find((d) => d.day === openDay) ?? null;
  const date = yearNode && monthNode && dayNode ? `${yearNode.year}-${monthNode.month}-${dayNode.day}` : null;

  useEffect(() => {
    setEntries(null);
    setEditing(null);
    if (!date) return;
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getDiaryEntries(token, date);
        if (active) setEntries(data?.entries ?? []);
      } catch (err) {
        if (active) setError(describeError(err, 'Entries unavailable'));
      }
    })();
    return () => {
      active = false;
    };
  }, [date, entriesVersion]);

  function toggleYear(id: string | null) {
    setOpenYear(id);
    setOpenMonth(null);
    setOpenDay(null);
    setParams({ diaryYear: id, diaryMonth: null, diaryDay: null });
  }

  function toggleMonth(id: string | null) {
    setOpenMonth(id);
    setOpenDay(null);
    setParams({ diaryMonth: id, diaryDay: null });
  }

  function toggleDay(id: string | null) {
    setOpenDay(id);
    setParams({ diaryDay: id });
  }

  async function handleCreate() {
    setSaving(true);
    try {
      const token = await getAccessToken();
      const created = await createDiaryEntry(token, newText);
      const [y, m, d] = created.date.split('-');
      setNewText('');
      setOpenYear(y);
      setOpenMonth(m);
      setOpenDay(d);
      setParams({ diaryYear: y, diaryMonth: m, diaryDay: d });
      setEntriesVersion((v) => v + 1);
      await refreshTree();
    } catch (err) {
      setError(describeError(err, 'Save failed'));
    } finally {
      setSaving(false);
    }
  }

  async function handleEditSave(entry: DiaryEntry) {
    if (!date) return;
    try {
      const token = await getAccessToken();
      await saveDiaryEntry(token, date, entry.name, draft);
      setEntries((prev) =>
        prev ? prev.map((e) => (e.name === entry.name ? { ...e, content: draft } : e)) : prev,
      );
      setEditing(null);
    } catch (err) {
      setError(describeError(err, 'Save failed'));
    }
  }

  function renderDay(): ReactNode {
    if (entries === null) return <p className="text-sm text-gray-500">Loading…</p>;
    const sorted = [...entries].sort((a, b) => a.time.localeCompare(b.time));
    return (
      <ul className="space-y-3">
        {sorted.map((entry) => (
          <li key={entry.name} className="text-sm">
            <div className="mb-1 flex items-center justify-between">
              <span className="font-mono text-xs text-gray-500">{entry.time}</span>
              {editing !== entry.name && (
                <button
                  type="button"
                  onClick={() => {
                    setEditing(entry.name);
                    setDraft(entry.content);
                  }}
                  className="text-xs text-gray-500 underline"
                >
                  Edit
                </button>
              )}
            </div>
            {editing === entry.name ? (
              <div>
                <textarea
                  aria-label="Edit entry"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  rows={6}
                  className="w-full rounded border border-gray-300 p-2 text-sm"
                />
                <div className="mt-1 flex gap-2">
                  <button
                    type="button"
                    onClick={() => handleEditSave(entry)}
                    className="rounded bg-gray-800 px-3 py-1 text-sm text-white"
                  >
                    Save
                  </button>
                  <button
                    type="button"
                    onClick={() => setEditing(null)}
                    className="rounded border border-gray-300 px-3 py-1 text-sm"
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : (
              <p className="whitespace-pre-wrap">{entry.content}</p>
            )}
          </li>
        ))}
      </ul>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Diary</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>

      <div className="mb-6">
        <label htmlFor="diary-new" className="mb-1 block text-sm font-medium">
          New entry
        </label>
        <textarea
          id="diary-new"
          value={newText}
          onChange={(e) => setNewText(e.target.value)}
          rows={4}
          className="w-full rounded border border-gray-300 p-2 text-sm"
        />
        <button
          type="button"
          onClick={handleCreate}
          disabled={saving || newText.trim() === ''}
          className="mt-2 rounded bg-gray-800 px-3 py-1 text-sm text-white disabled:opacity-50"
        >
          Save
        </button>
      </div>

      {error && <p className="mb-4 text-sm text-red-600">{error}</p>}

      {years && (
        <Accordion
          openId={yearNode ? yearNode.year : null}
          onToggle={toggleYear}
          items={newestFirst(years, (y) => y.year).map((y) => ({
            id: y.year,
            label: y.year,
            badge: String(sum(y.months.flatMap((m) => m.days.map((d) => d.count)))),
            content: (
              <Accordion
                openId={monthNode ? monthNode.month : null}
                onToggle={toggleMonth}
                items={newestFirst(y.months, (m) => m.month).map((m) => ({
                  id: m.month,
                  label: monthName(m.month),
                  badge: String(sum(m.days.map((d) => d.count))),
                  content: (
                    <Accordion
                      openId={dayNode ? dayNode.day : null}
                      onToggle={toggleDay}
                      items={newestFirst(m.days, (d) => d.day).map((d) => ({
                        id: d.day,
                        label: d.day,
                        badge: String(d.count),
                        content: renderDay(),
                      }))}
                    />
                  ),
                }))}
              />
            ),
          }))}
        />
      )}
    </div>
  );
}
