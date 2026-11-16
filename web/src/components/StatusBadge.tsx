import {Badge} from '@astryxdesign/core/Badge';
import {statusTone} from '../lib/format';

export function StatusBadge({status}: {status: string}) {
  return <Badge variant={statusTone(status)} label={status} />;
}
