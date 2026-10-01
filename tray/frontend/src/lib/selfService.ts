import type { RunStatus, SelfServiceEvent, SelfServiceRun, SelfServiceTask } from './types';

export const RUN_STATUS_LABEL: Record<RunStatus, string> = {
  running: 'Em andamento',
  ok: 'Concluído',
  warning: 'Concluído com avisos',
  error: 'Falhou',
  cancelled: 'Cancelado',
  timeout: 'Tempo esgotado',
};

/** Execucao acompanhada pela interface. */
export interface ActiveRun {
  runId: string;
  task: SelfServiceTask;
  status: RunStatus;
  progress: number;
  message: string;
}

export function isFinalStatus(status: RunStatus): boolean {
  return status !== 'running';
}

function clampProgress(n: number): number {
  return Math.min(100, Math.max(0, Math.round(n)));
}

/** Aplica um evento do hub; depois do fim, eventos atrasados sao ignorados e o progresso nunca volta. */
export function applyEvent(run: ActiveRun, e: SelfServiceEvent): ActiveRun {
  if (e.runId !== run.runId || isFinalStatus(run.status)) return run;
  const final = isFinalStatus(e.status);
  return {
    ...run,
    status: e.status,
    progress: final ? 100 : Math.max(run.progress, clampProgress(e.progress)),
    message: e.message || run.message,
  };
}

/** Aplica o estado lido da API (usado na partida e enquanto o tempo real estiver fora). */
export function applyRun(run: ActiveRun, r: SelfServiceRun): ActiveRun {
  return applyEvent(run, { runId: r.runId, status: r.status, progress: r.progress, message: r.messages.at(-1) ?? '' });
}

export interface TicketDraft {
  title: string;
  description: string;
}

/** Chamado sugerido quando a acao termina sem resolver o problema. */
export function suggestedTicket(run: ActiveRun): TicketDraft {
  const title = `Problema não resolvido: ${run.task.label}`.slice(0, 200);
  const lines = [
    `Tentei resolver sozinho com a ação "${run.task.label}", mas o problema continua.`,
    `Resultado: ${RUN_STATUS_LABEL[run.status]}.`,
  ];
  if (run.message) lines.push(`Última mensagem: ${run.message}`);
  lines.push('', 'Descreva aqui o que está acontecendo:');
  return { title, description: lines.join('\n') };
}
