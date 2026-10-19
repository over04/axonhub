import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Skeleton } from '@/components/ui/skeleton';
import { useGeneralSettings } from '@/features/system/data/system';
import type { AnalyticsDimensionStat } from '../data/analytics';

function formatExactNumber(value: number): string {
  return Math.round(value).toLocaleString();
}

const COMPACT_UNITS = [
  { limit: 1e12, en: 'T', zh: '万亿' },
  { limit: 1e8, en: 'B', zh: '亿' },
  { limit: 1e4, en: 'M', zh: '万' },
] as const;

function formatCompactNumber(value: number, isZh: boolean): string {
  const abs = Math.abs(value);
  for (const unit of COMPACT_UNITS) {
    if (abs >= unit.limit) {
      const scaled = value / unit.limit;
      const digits = Math.abs(scaled) >= 100 ? 0 : Math.abs(scaled) >= 10 ? 1 : 2;
      return `${scaled.toFixed(digits)}${isZh ? unit.zh : unit.en}`;
    }
  }
  return Math.round(value).toLocaleString();
}

type Dimension = 'channel' | 'model' | 'apiKey' | 'user';

interface DimensionDetailTableProps {
  channelStats: AnalyticsDimensionStat[];
  modelStats: AnalyticsDimensionStat[];
  apiKeyStats: AnalyticsDimensionStat[];
  userStats: AnalyticsDimensionStat[];
  isLoading: boolean;
}

export function DimensionDetailTable({
  channelStats,
  modelStats,
  apiKeyStats,
  userStats,
  isLoading,
}: DimensionDetailTableProps) {
  const { t, i18n } = useTranslation();
  const [dimension, setDimension] = useState<Dimension>('channel');
  const { data: generalSettings } = useGeneralSettings();

  const currencyCode = generalSettings?.currencyCode || 'USD';
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const isZh = i18n.language.startsWith('zh');

  const formatCurrency = useCallback(
    (val: number) =>
      t('currencies.format', {
        val,
        currency: currencyCode,
        locale,
        minimumFractionDigits: 4,
        maximumFractionDigits: 4,
      }),
    [currencyCode, locale, t]
  );

  const dimensionData: Record<Dimension, AnalyticsDimensionStat[]> = {
    channel: channelStats,
    model: modelStats,
    apiKey: apiKeyStats,
    user: userStats,
  };

  const currentData = dimensionData[dimension] || [];

  if (isLoading) {
    return (
      <Card className='hover-card'>
        <CardHeader>
          <Skeleton className='h-6 w-[200px]' />
        </CardHeader>
        <CardContent>
          <Skeleton className='h-[300px] w-full' />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className='hover-card'>
      <CardHeader className='flex flex-row items-center justify-between'>
        <CardTitle>{t('analytics.table.title')}</CardTitle>
        <Tabs value={dimension} onValueChange={(v) => setDimension(v as Dimension)}>
          <TabsList className='h-8'>
            <TabsTrigger value='channel' className='text-xs'>
              {t('analytics.table.channel')}
            </TabsTrigger>
            <TabsTrigger value='model' className='text-xs'>
              {t('analytics.table.model')}
            </TabsTrigger>
            <TabsTrigger value='apiKey' className='text-xs'>
              {t('analytics.table.apiKey')}
            </TabsTrigger>
            <TabsTrigger value='user' className='text-xs'>
              {t('analytics.table.user')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        {currentData.length === 0 ? (
          <div className='flex h-[200px] items-center justify-center text-sm text-muted-foreground'>
            {t('analytics.table.noData')}
          </div>
        ) : (
          <div className='-mx-2 overflow-x-auto'>
            <table className='w-full min-w-max border-collapse text-sm'>
              <thead>
                <tr>
                  <th className='sticky left-0 z-20 whitespace-nowrap border-b bg-card px-3 pb-2 pt-1 text-left text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.name')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.totalTokens')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.inputTokens')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.cachedTokens')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.uncachedTokens')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.outputTokens')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.cacheHitRate')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.requests')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.tokensPerSecond')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.ttft')}
                  </th>
                  <th className='whitespace-nowrap border-b px-3 pb-2 pt-1 text-right text-xs font-medium text-muted-foreground'>
                    {t('analytics.table.cost')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {currentData.map((item) => {
                  const cacheHitRate = item.inputTokens > 0 ? (item.cachedInputTokens / item.inputTokens) * 100 : 0;
                  return (
                    <tr key={item.id} className='group transition-colors hover:bg-muted/40'>
                      <td
                        className='sticky left-0 z-10 max-w-[220px] truncate border-b bg-card px-3 py-2.5 text-sm font-medium group-hover:bg-muted/40'
                        title={item.name}
                      >
                        {item.name}
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm font-semibold tabular-nums'
                        title={formatExactNumber(item.totalTokens)}
                      >
                        {formatCompactNumber(item.totalTokens, isZh)}
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'
                        title={formatExactNumber(item.inputTokens)}
                      >
                        {formatCompactNumber(item.inputTokens, isZh)}
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'
                        title={formatExactNumber(item.cachedInputTokens)}
                      >
                        {formatCompactNumber(item.cachedInputTokens, isZh)}
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'
                        title={formatExactNumber(item.inputTokens - item.cachedInputTokens)}
                      >
                        {formatCompactNumber(item.inputTokens - item.cachedInputTokens, isZh)}
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'
                        title={formatExactNumber(item.outputTokens)}
                      >
                        {formatCompactNumber(item.outputTokens, isZh)}
                      </td>
                      <td className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'>
                        {cacheHitRate.toFixed(1)}%
                      </td>
                      <td
                        className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums'
                        title={formatExactNumber(item.requestCount)}
                      >
                        {formatCompactNumber(item.requestCount, isZh)}
                      </td>
                      <td className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'>
                        {item.tokensPerSecond == null ? '—' : item.tokensPerSecond.toFixed(2)}
                      </td>
                      <td className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm tabular-nums text-muted-foreground'>
                        {item.ttftMs == null ? '—' : Math.round(item.ttftMs).toLocaleString()}
                      </td>
                      <td className='whitespace-nowrap border-b px-3 py-2.5 text-right text-sm font-medium tabular-nums'>
                        {formatCurrency(item.cost)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
