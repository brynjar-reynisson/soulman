import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { search, ApiError, type SearchResult } from '../api';
import { getParam, setParams } from '../urlState';

export function SearchPage({ onBack }: { onBack: () => void }) {
  const [query, setQuery] = useState(getParam('q') ?? '');
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const runSearch = async (q: string) => {
    if (!q.trim()) return;
    setLoading(true);
    setError(null);
    const token = await getAccessToken();
    try {
      const data = await search(token, q);
      setResults(data.results);
      setParams({ page: 'search', q });
    } catch (err) {
      // Deliberately leave any previously-rendered `results` untouched here:
      // results are only replaced on a new successful search, so a
      // transient failure shows the error banner above the prior results
      // instead of blanking out what the user was just looking at.
      if (err instanceof ApiError && err.status === 503) {
        setError('Web search is not configured');
      } else {
        setError('Web search failed');
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (query) runSearch(query);
    // Mount-only effect (deliberately empty deps). oxlint recognizes this
    // eslint-style directive comment — it aims for eslint-comment
    // compatibility — and actually suppresses react-hooks/exhaustive-deps
    // with it, despite this project's .oxlintrc.json not listing
    // "react-hooks" among its plugins: removing the line below surfaces a
    // real warning under `npm run lint`, verified empirically.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Empty-state box (pre-search) is rendered larger — a google.com-style
  // centered search box — so it reads as visually distinct from the
  // compact, top-pinned bar shown once results exist, not just repositioned.
  const renderSearchBox = (size: 'large' | 'compact') => (
    <form
      className="flex gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        runSearch(query);
      }}
    >
      <label htmlFor="search-query" className="sr-only">
        Search query
      </label>
      <input
        id="search-query"
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className={`flex-1 rounded border ${size === 'large' ? 'px-4 py-3 text-base' : 'px-3 py-2 text-sm'}`}
        placeholder="Search the web..."
      />
      <button
        type="submit"
        className={`rounded border bg-white ${size === 'large' ? 'px-6 py-3 text-base' : 'px-4 py-2 text-sm'}`}
      >
        Search
      </button>
    </form>
  );

  if (results === null) {
    return (
      <div className="min-h-screen bg-gray-50 p-6">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-2xl font-semibold">Web-Search</h1>
          <button onClick={onBack} className="text-sm text-gray-500 underline">
            ← Soulman
          </button>
        </div>
        <div className="mx-auto mt-24 max-w-xl">
          <h2 className="mb-6 text-center text-xl font-medium">Soulman Search</h2>
          {renderSearchBox('large')}
          {loading && <p className="mt-4 text-center text-sm text-gray-500">Searching…</p>}
          {error && <p className="mt-4 text-center text-sm text-red-600">{error}</p>}
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Web-Search</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <div className="mx-auto max-w-2xl">
        <div className="mb-4">{renderSearchBox('compact')}</div>
        {loading && <p className="text-sm text-gray-500">Searching…</p>}
        {error && <p className="text-sm text-red-600">{error}</p>}
        {!loading && !error && results.length === 0 && (
          <p className="text-sm text-gray-500">No results found.</p>
        )}
        <ul className="space-y-4">
          {results.map((r, i) => (
            <li key={`${r.url}-${i}`}>
              <a href={r.url} target="_blank" rel="noopener noreferrer" className="text-blue-700 underline">
                {r.title}
              </a>
              <p className="text-xs text-gray-400">{r.url}</p>
              <p className="text-sm text-gray-700">{r.snippet}</p>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
