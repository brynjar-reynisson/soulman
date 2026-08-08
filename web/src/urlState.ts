// Mirrors navigation state into the URL query string so it survives page
// reloads — mobile browsers routinely reload the tab after the screen turns
// back on or after switching apps, which would otherwise drop in-memory
// React state and strand the user back on the dashboard.

export function getParam(name: string): string | null {
  return new URLSearchParams(window.location.search).get(name);
}

export function setParams(updates: Record<string, string | null>): void {
  const params = new URLSearchParams(window.location.search);
  for (const [key, value] of Object.entries(updates)) {
    if (value === null) params.delete(key);
    else params.set(key, value);
  }
  const query = params.toString();
  const url = query ? `${window.location.pathname}?${query}` : window.location.pathname;
  window.history.replaceState(null, '', url);
}
