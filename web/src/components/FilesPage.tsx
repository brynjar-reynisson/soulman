import { FileRootList } from './FileRootList';

export function FilesPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Files</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <FileRootList />
    </div>
  );
}
