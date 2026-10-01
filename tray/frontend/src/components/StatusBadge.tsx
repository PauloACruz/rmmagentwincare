import { STATUS_LABEL } from '../lib/format';
import type { TicketStatus } from '../lib/types';

export function StatusBadge({ status }: { status: TicketStatus }) {
  return <span className={`badge badge-${status}`}>{STATUS_LABEL[status]}</span>;
}
