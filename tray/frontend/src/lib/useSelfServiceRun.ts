import { useEffect, useState } from 'react';
import { applyEvent, applyRun, type ActiveRun } from './selfService';
import type { Backend, SelfServiceTask } from './types';

// Consulta de seguranca caso algum evento do tempo real se perca.
const POLL_MS = 10_000;

export function useSelfServiceRun(backend: Backend) {
  const [run, setRun] = useState<ActiveRun | null>(null);
  const runId = run?.runId ?? '';
  const running = run?.status === 'running';

  useEffect(
    () => backend.onSelfService((e) => { setRun((r) => (r ? applyEvent(r, e) : r)); }),
    [backend],
  );

  useEffect(() => {
    if (!runId || !running) return;
    let alive = true;
    const poll = () => {
      backend.selfServiceRun(runId).then(
        (state) => {
          if (alive) setRun((r) => (r ? applyRun(r, state) : r));
        },
        () => undefined,
      );
    };
    poll();
    const timer = setInterval(poll, POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [backend, runId, running]);

  async function start(task: SelfServiceTask) {
    const { runId: id } = await backend.runSelfService(task.module, task.key);
    setRun({ runId: id, task, status: 'running', progress: 0, message: '' });
  }

  return { run, start, clear: () => { setRun(null); } };
}
