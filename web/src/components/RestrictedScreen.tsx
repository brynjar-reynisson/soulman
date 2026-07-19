export function RestrictedScreen({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 bg-gray-50">
      <p className="text-lg text-gray-700">This is a private system.</p>
      <button onClick={onSignOut} className="text-sm text-gray-500 underline">
        Sign out
      </button>
    </div>
  );
}
