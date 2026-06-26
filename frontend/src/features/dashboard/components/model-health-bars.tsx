import { memo } from 'react';
import { format } from 'date-fns';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { ModelHealthPoint } from '../data/model-availability';

// ModelHealthBars renders the per-day availability column: one vertical bar
// per day, colored by that day's success rate (green/yellow/red/gray), with a
// tooltip showing the day's stats. Mirrors the /channels health cell.
function ModelHealthBarsBase({ points }: { points: ModelHealthPoint[] }) {
  const { t } = useTranslation();

  if (!points || points.length === 0) {
    return <span className='text-muted-foreground text-xs'>-</span>;
  }

  // Same as /channels ChannelHealthCell: show the most recent buckets.
  const maxBars = 15;
  const displayPoints = points.slice(-maxBars);

  return (
    <div className='flex items-center gap-0.5'>
      {displayPoints.map((point, index) => {
        const hasRequests = point.totalRequests > 0;
        const successRate = hasRequests ? point.successRequests / point.totalRequests : 0;

        const isHealthy = hasRequests && successRate >= 0.9;
        const isWarning = hasRequests && successRate >= 0.5 && successRate < 0.9;
        const isError = hasRequests && successRate < 0.5;
        const isIdle = !hasRequests;

        const day = format(new Date(point.timestamp * 1000), 'MM-dd HH:mm');

        return (
          <Tooltip key={`${point.timestamp}-${index}`}>
            <TooltipTrigger asChild>
              <div
                className={cn(
                  'h-8 w-1.5 cursor-help rounded-sm',
                  isHealthy && 'bg-green-500',
                  isWarning && 'bg-yellow-500',
                  isError && 'bg-red-500',
                  isIdle && 'bg-gray-200'
                )}
              />
            </TooltipTrigger>
            <TooltipContent>
              <div className='space-y-1 text-xs'>
                <div>{t('projectUsage.modelAvailability.tooltipDay')}: {day}</div>
                <div>
                  {t('projectUsage.modelAvailability.tooltipSuccess')}:{' '}
                  {point.successRequests}/{point.totalRequests}
                </div>
                <div>
                  {t('projectUsage.modelAvailability.tooltipLatency')}:{' '}
                  {point.avgLatencyMs != null ? formatDuration(point.avgLatencyMs) : '-'}
                </div>
              </div>
            </TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}

export const ModelHealthBars = memo(ModelHealthBarsBase);
