import {act, renderHook} from '@testing-library/react';
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {usePolling} from './usePolling';

describe('usePolling', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('fetches immediately and on every interval, then stops on unmount', async () => {
    const fetcher = vi.fn().mockResolvedValue('v');
    const {result, unmount} = renderHook(() => usePolling(fetcher, 10_000));
    await act(async () => {});
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(result.current.data).toBe('v');
    expect(result.current.loading).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(fetcher).toHaveBeenCalledTimes(2);
    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('keeps last data when a refresh fails and clears the error on recovery', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce('first').mockRejectedValueOnce(new Error('down')).mockResolvedValue('back');
    const {result} = renderHook(() => usePolling(fetcher, 1000));
    await act(async () => {});
    expect(result.current.data).toBe('first');
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(result.current.data).toBe('first');
    expect(result.current.error?.message).toBe('down');
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(result.current.data).toBe('back');
    expect(result.current.error).toBeNull();
  });

  it('does not start a new request while one is in flight', async () => {
    let resolve!: (v: string) => void;
    const fetcher = vi.fn(() => new Promise<string>((r) => { resolve = r; }));
    renderHook(() => usePolling(fetcher, 1000));
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => { resolve('x'); });
  });
});

describe('usePolling: fetcher changes', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('refetches immediately when the fetcher identity changes and ignores stale responses', async () => {
    let resolveOld!: (v: string) => void;
    const oldFetcher = vi.fn(() => new Promise<string>((r) => { resolveOld = r; }));
    const newFetcher = vi.fn().mockResolvedValue('new');
    const {result, rerender} = renderHook(({f}) => usePolling(f, 60_000), {initialProps: {f: oldFetcher as () => Promise<string>}});
    await act(async () => {});
    rerender({f: newFetcher});
    await act(async () => {});
    expect(newFetcher).toHaveBeenCalledTimes(1);
    expect(result.current.data).toBe('new');
    await act(async () => { resolveOld('old'); });
    expect(result.current.data).toBe('new');
  });
});
