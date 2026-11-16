import {useCallback, useEffect, useRef, useState} from 'react';

export type Polled<T> = {
  data: T | null;
  error: Error | null;
  loading: boolean;
  reload: () => void;
};

/**
 * Wywołuje fetcher od razu i co intervalMs (cykliczne odświeżenia nie nakładają się).
 * Zmiana tożsamości fetchera (np. nowe filtry/strona) natychmiast odpytuje ponownie,
 * a wynik starszego żądania nigdy nie nadpisuje nowszego.
 * Poprzednie dane zostają widoczne, gdy odświeżenie się nie powiedzie.
 */
export function usePolling<T>(fetcher: () => Promise<T>, intervalMs: number): Polled<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;
  const inFlight = useRef(0);
  const seq = useRef(0);
  const alive = useRef(true);

  const run = useCallback(async (force: boolean) => {
    if (inFlight.current > 0 && !force) return;
    const mine = ++seq.current;
    inFlight.current++;
    try {
      const d = await fetcherRef.current();
      if (alive.current && mine === seq.current) {
        setData(d);
        setError(null);
      }
    } catch (e) {
      if (alive.current && mine === seq.current) setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      inFlight.current--;
      if (alive.current && mine === seq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    const id = setInterval(() => void run(false), intervalMs);
    return () => {
      alive.current = false;
      clearInterval(id);
    };
  }, [run, intervalMs]);

  // pierwsze pobranie oraz każda zmiana fetchera
  useEffect(() => {
    void run(true);
  }, [run, fetcher]);

  return {data, error, loading, reload: () => void run(true)};
}
