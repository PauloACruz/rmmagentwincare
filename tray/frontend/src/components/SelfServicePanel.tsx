import type { ActiveRun } from '../lib/selfService';
import type { SelfServiceTask } from '../lib/types';
import { SelfServiceList } from './SelfServiceList';
import { SelfServiceProgress } from './SelfServiceProgress';

interface Props {
  tasks: SelfServiceTask[];
  run: ActiveRun | null;
  onRun: (task: SelfServiceTask) => Promise<void>;
  onOpenTicket: () => void;
  onDismiss: () => void;
}

export function SelfServicePanel({ tasks, run, onRun, onOpenTicket, onDismiss }: Props) {
  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Resolver sozinho</h2>
      </div>
      <p className="muted ss-intro">Ações liberadas pelo suporte para você resolver problemas comuns sem esperar um técnico.</p>
      {run && <SelfServiceProgress run={run} onOpenTicket={onOpenTicket} onDismiss={onDismiss} />}
      <SelfServiceList tasks={tasks} locked={run?.status === 'running'} onRun={onRun} />
    </section>
  );
}
