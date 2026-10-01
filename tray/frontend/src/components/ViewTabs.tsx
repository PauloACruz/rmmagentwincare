export type Tab = 'tickets' | 'selfService';

interface Props {
  active: Tab;
  running: boolean;
  onChange: (tab: Tab) => void;
}

export function ViewTabs({ active, running, onChange }: Props) {
  const tab = (id: Tab, label: string) => (
    <button
      type="button"
      className={`tab${active === id ? ' tab-active' : ''}`}
      aria-current={active === id ? 'page' : undefined}
      onClick={() => { onChange(id); }}
    >
      {label}
      {id === 'selfService' && running && <span className="tab-dot" aria-label="Ação em andamento" title="Ação em andamento" />}
    </button>
  );
  return (
    <nav className="tabs" aria-label="Seções">
      {tab('tickets', 'Meus chamados')}
      {tab('selfService', 'Resolver sozinho')}
    </nav>
  );
}
