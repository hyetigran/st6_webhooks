import { createContext, useContext, useState, type ReactNode } from "react";

const STORAGE_KEY = "gauntlet-relay:api-key";

interface AuthContextValue {
  apiKey: string | null;
  signIn: (apiKey: string) => void;
  signOut: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [apiKey, setApiKey] = useState<string | null>(() => localStorage.getItem(STORAGE_KEY));

  const signIn = (key: string) => {
    localStorage.setItem(STORAGE_KEY, key);
    setApiKey(key);
  };

  const signOut = () => {
    localStorage.removeItem(STORAGE_KEY);
    setApiKey(null);
  };

  return <AuthContext.Provider value={{ apiKey, signIn, signOut }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
