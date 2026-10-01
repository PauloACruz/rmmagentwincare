import { createContext, useContext } from 'react';
import type { Backend } from './types';

export const BackendContext = createContext<Backend | null>(null);

export function useBackend(): Backend {
  const b = useContext(BackendContext);
  if (!b) throw new Error('BackendContext ausente');
  return b;
}

export async function loadBackend(): Promise<Backend> {
  if (import.meta.env.MODE === 'mock') {
    return (await import('./mockBackend')).mockBackend;
  }
  return (await import('./wailsBackend')).wailsBackend;
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error && e.message) return e.message;
  if (typeof e === 'string' && e) return e;
  return 'Algo deu errado. Tente novamente.';
}
