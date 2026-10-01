import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { applyEvent, type ActiveRun } from '../lib/selfService';
import type { SelfServiceTask } from '../lib/types';
import { SelfServiceList } from './SelfServiceList';
import { SelfServiceProgress } from './SelfServiceProgress';

afterEach(cleanup);

const task: SelfServiceTask = { module: 'cleanup', key: 'temp', label: 'Limpar arquivos temporários', description: 'Libera espaço em disco.' };

describe('SelfServiceList', () => {
  it('pede confirmação antes de executar e mostra o erro de computador ocupado', async () => {
    const busy = 'Já existe uma manutenção em andamento neste computador. Tente novamente em alguns minutos.';
    const onRun = vi.fn(() => Promise.reject(new Error(busy)));
    render(<SelfServiceList tasks={[task]} locked={false} onRun={onRun} />);
    expect(screen.getByText('Libera espaço em disco.')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Executar Limpar arquivos temporários' }));
    expect(onRun).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }));
    expect(onRun).toHaveBeenCalledWith(task);
    expect((await screen.findByRole('alert')).textContent).toBe(busy);
  });
});

describe('SelfServiceProgress', () => {
  const running: ActiveRun = { runId: 'r1', task, status: 'running', progress: 0, message: '' };

  it('mostra o progresso em tempo real e oferece abrir chamado quando falha', () => {
    const onOpenTicket = vi.fn();
    const step = applyEvent(running, { runId: 'r1', status: 'running', progress: 40, message: 'Limpando arquivos' });
    const { rerender } = render(<SelfServiceProgress run={step} onOpenTicket={onOpenTicket} onDismiss={vi.fn()} />);
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('40');
    expect(screen.getByText('Limpando arquivos')).toBeTruthy();
    expect(screen.getByText('Em andamento')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Abrir chamado' })).toBeNull();

    const failed = applyEvent(step, { runId: 'r1', status: 'error', progress: 60, message: 'Falha ao limpar' });
    rerender(<SelfServiceProgress run={failed} onOpenTicket={onOpenTicket} onDismiss={vi.fn()} />);
    expect(screen.getByText('Falhou')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Abrir chamado' }));
    expect(onOpenTicket).toHaveBeenCalled();
  });

  it('mostra Concluído sem o botão de chamado quando dá certo', () => {
    const done = applyEvent(running, { runId: 'r1', status: 'ok', progress: 100, message: 'Tudo certo.' });
    render(<SelfServiceProgress run={done} onOpenTicket={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.getByText('Concluído')).toBeTruthy();
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('100');
    expect(screen.queryByRole('button', { name: 'Abrir chamado' })).toBeNull();
  });
});
