export function LoginScreen({ onSignIn }: { onSignIn: () => void }) {
  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <button
        onClick={onSignIn}
        className="rounded bg-blue-600 px-6 py-3 text-white hover:bg-blue-700"
      >
        Sign in with Google
      </button>
    </div>
  );
}
