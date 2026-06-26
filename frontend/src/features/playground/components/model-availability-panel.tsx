import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconChartBar } from '@tabler/icons-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { usePermissions } from '@/hooks/usePermissions';
import {
  useModelAvailability,
  type ModelAvailabilityScope,
} from '../data/model-availability';

// ModelAvailabilityPanel shows a sanitized, channel-free view of model
// availability (capabilities, success rate, avg latency). Safe for registered
// users — no channel/provider/pricing information is exposed. Registered users
// toggle between their own project's usage and the global system view; owners
// always see the global view (the toggle is meaningless for them).
export function ModelAvailabilityPanel() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<ModelAvailabilityScope>('project');
  const { isOwner } = usePermissions();

  // owners see everything; the project/global toggle is meaningless for them.
  const effectiveScope: ModelAvailabilityScope = isOwner ? 'global' : scope;
  const { data, isLoading } = useModelAvailability(effectiveScope);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant='outline' size='sm' className='w-full'>
          <IconChartBar className='mr-2 h-4 w-4' />
          {t('playground.modelAvailability.button')}
        </Button>
      </DialogTrigger>
      <DialogContent className='max-h-[80vh] max-w-2xl overflow-auto'>
        <DialogHeader>
          <DialogTitle>{t('playground.modelAvailability.title')}</DialogTitle>
        </DialogHeader>
        {!isOwner && (
          <div className='flex gap-2'>
            <Button
              size='sm'
              variant={scope === 'project' ? 'default' : 'outline'}
              onClick={() => setScope('project')}
            >
              {t('playground.modelAvailability.myUsage')}
            </Button>
            <Button
              size='sm'
              variant={scope === 'global' ? 'default' : 'outline'}
              onClick={() => setScope('global')}
            >
              {t('playground.modelAvailability.global')}
            </Button>
          </div>
        )}
        {isLoading ? (
          <p className='text-muted-foreground'>{t('common.loading')}</p>
        ) : (
          <div className='space-y-2'>
            {(data ?? []).map((m) => (
              <div
                key={m.modelId}
                className='bg-muted/30 flex items-center justify-between rounded-lg border p-3'
              >
                <div className='min-w-0'>
                  <p className='truncate text-sm font-medium'>{m.displayName}</p>
                  <div className='mt-1 flex flex-wrap gap-1'>
                    {m.capabilities?.vision && (
                      <Badge variant='outline' className='text-[10px]'>
                        Vision
                      </Badge>
                    )}
                    {m.capabilities?.toolCall && (
                      <Badge variant='outline' className='text-[10px]'>
                        Tool
                      </Badge>
                    )}
                    {m.capabilities?.reasoning && (
                      <Badge variant='outline' className='text-[10px]'>
                        Reasoning
                      </Badge>
                    )}
                  </div>
                </div>
                <div className='text-muted-foreground ml-2 shrink-0 text-right text-xs'>
                  {m.successRate != null && <div>{(m.successRate * 100).toFixed(1)}%</div>}
                  {m.avgLatencyMs != null && <div>{Math.round(m.avgLatencyMs)}ms</div>}
                </div>
              </div>
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
