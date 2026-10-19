import { useMemo, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { BarChart3, Brain, Key, Zap } from 'lucide-react';
import { Header } from '@/components/layout/header';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { TimePeriodSelector, type TimePeriod } from '@/components/time-period-selector';
import { formatNumber } from '@/utils/format-number';
import { useAnalyticsFilterStore } from '@/stores/analyticsStore';
import { useAnalyticsMetadata, useAnalyticsOverview, useAnalyticsDailyStats, useAnalyticsDimensionStats } from './data/analytics';
import { AnalyticsFilterBar } from './components/analytics-filter-bar';
import { OverviewCards } from './components/overview-cards';
import { CombinedTrendChart } from './components/combined-trend-chart';
import { DimensionPieCharts } from './components/dimension-pie-charts';
import { DimensionDetailTable } from './components/dimension-detail-table';
import { useGeneralSettings } from '@/features/system/data/system';
// 全局统计区块（渠道 / 模型 / API Key / 性能）自仪表板迁入，复用其组件与数据 hook。
import { CollapsibleSection } from '@/features/dashboard/components/collapsible-section';
import { DailyRequestStats } from '@/features/dashboard/components/daily-requests-stats';
import { ChannelSuccessRate } from '@/features/dashboard/components/channel-success-rate';
import { RequestsByChannelChart } from '@/features/dashboard/components/requests-by-channel-chart';
import { TokensByChannelChart } from '@/features/dashboard/components/tokens-by-channel-chart';
import { RequestsByModelChart } from '@/features/dashboard/components/requests-by-model-chart';
import { TokensByModelChart } from '@/features/dashboard/components/tokens-by-model-chart';
import { RequestsByAPIKeyChart } from '@/features/dashboard/components/requests-by-api-key-chart';
import { TokensByAPIKeyChart } from '@/features/dashboard/components/tokens-by-api-key-chart';
import { ModelPerformanceStats } from '@/features/dashboard/components/model-performance-stats';
import { ChannelPerformanceStats } from '@/features/dashboard/components/channel-performance-stats';
import { FastestModelsCard } from '@/features/dashboard/components/fastest-models-card';
import { FastestChannelsCard } from '@/features/dashboard/components/fastest-channels-card';

export default function AnalyticsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const filter = useAnalyticsFilterStore((state) => state.filter);
  const { data: generalSettings } = useGeneralSettings();

  const currencyCode = generalSettings?.currencyCode || 'USD';

  const { data: metadata } = useAnalyticsMetadata();
  const { data: overview, isLoading: isOverviewLoading } = useAnalyticsOverview(filter);
  const { data: dailyStats, isLoading: isDailyLoading } = useAnalyticsDailyStats(filter);
  const { data: channelStats, isLoading: isChannelLoading } = useAnalyticsDimensionStats(filter, 'channel');
  const { data: modelStats, isLoading: isModelLoading } = useAnalyticsDimensionStats(filter, 'model');
  const { data: apiKeyStats, isLoading: isApiKeyLoading } = useAnalyticsDimensionStats(filter, 'apiKey');
  const { data: userStats, isLoading: isUserLoading } = useAnalyticsDimensionStats(filter, 'user');

  // 全局统计区块（原仪表板 owner 视图）的时间范围与性能描述。
  const [channelTimePeriod, setChannelTimePeriod] = useState<TimePeriod>('allTime');
  const [channelTokensTimePeriod, setChannelTokensTimePeriod] = useState<TimePeriod>('allTime');
  const [modelTimePeriod, setModelTimePeriod] = useState<TimePeriod>('allTime');
  const [modelTokensTimePeriod, setModelTokensTimePeriod] = useState<TimePeriod>('allTime');
  const [apiKeyTimePeriod, setApiKeyTimePeriod] = useState<TimePeriod>('allTime');
  const [apiKeyTokensTimePeriod, setApiKeyTokensTimePeriod] = useState<TimePeriod>('allTime');
  const [modelTotalRequests, setModelTotalRequests] = useState(0);
  const [channelTotalRequests, setChannelTotalRequests] = useState(0);

  const modelPerformanceDescription = useMemo(() => {
    return t('dashboard.charts.performanceDescription', { count: formatNumber(modelTotalRequests) });
  }, [t, modelTotalRequests]);

  const channelPerformanceDescription = useMemo(() => {
    return t('dashboard.charts.performanceDescription', { count: formatNumber(channelTotalRequests) });
  }, [t, channelTotalRequests]);

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header />
      <Button onClick={() => navigate({ to: '/' })} variant='outline' className='self-start'>
        {t('dashboard.channelSuccessRates.backToDashboard')}
      </Button>

      <div className='space-y-1'>
        <h1 className='text-2xl font-bold tracking-tight'>{t('analytics.title')}</h1>
        <p className='text-muted-foreground text-sm'>{t('analytics.description')}</p>
      </div>

      <AnalyticsFilterBar earliestDate={metadata?.earliestDate} />
      <OverviewCards overview={overview} isLoading={isOverviewLoading} />

      {/* 全局概览（自仪表板迁入）。总请求/成功率/Token 等汇总指标已由上方
          跟随筛选条件的 OverviewCards 提供，此处不再重复展示四张全时段卡片。 */}
      <section className='space-y-4'>
        <h2 className='text-lg font-semibold tracking-tight'>{t('analytics.sections.globalOverview')}</h2>
        <div className='grid gap-4 md:grid-cols-2 lg:grid-cols-7'>
          <Card className='hover-card col-span-1 lg:col-span-4'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.dailyRequestOverview')}</CardTitle>
            </CardHeader>
            <CardContent className='pl-2'>
              <DailyRequestStats />
            </CardContent>
          </Card>
          <Card className='hover-card col-span-1 lg:col-span-3'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.channelSuccessRate')}</CardTitle>
              <CardDescription>{t('dashboard.charts.channelSuccessRateDescription')}</CardDescription>
              <CardAction>
                <Link to='/dashboard/channel-success-rates' className='text-sm text-primary hover:underline'>
                  {t('dashboard.viewAll')}
                </Link>
              </CardAction>
            </CardHeader>
            <CardContent>
              <ChannelSuccessRate />
            </CardContent>
          </Card>
        </div>
      </section>

      <CombinedTrendChart data={dailyStats || []} isLoading={isDailyLoading} currencyCode={currencyCode} />
      <DimensionPieCharts
        channelStats={channelStats || []}
        modelStats={modelStats || []}
        apiKeyStats={apiKeyStats || []}
        userStats={userStats || []}
        isLoading={isChannelLoading || isModelLoading || isApiKeyLoading || isUserLoading}
        currencyCode={currencyCode}
      />
      <DimensionDetailTable
        channelStats={channelStats || []}
        modelStats={modelStats || []}
        apiKeyStats={apiKeyStats || []}
        userStats={userStats || []}
        isLoading={isChannelLoading || isModelLoading || isApiKeyLoading || isUserLoading}
      />

      {/* 渠道明细（自仪表板迁入） */}
      <CollapsibleSection
        storagePrefix='analytics-section'
        title={t('dashboard.sections.channels')}
        icon={<BarChart3 className='h-4 w-4 text-primary' />}
        storageKey='channels'
      >
        <div className='grid gap-4 md:grid-cols-2'>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.requestsCostByChannel')}</CardTitle>
              <CardDescription>{t('dashboard.charts.requestsCostByChannelDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={channelTimePeriod} onChange={setChannelTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <RequestsByChannelChart timePeriod={channelTimePeriod} />
            </CardContent>
          </Card>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.tokensByChannel')}</CardTitle>
              <CardDescription>{t('dashboard.charts.tokensByChannelDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={channelTokensTimePeriod} onChange={setChannelTokensTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <TokensByChannelChart timePeriod={channelTokensTimePeriod} />
            </CardContent>
          </Card>
        </div>
      </CollapsibleSection>

      {/* 模型明细（自仪表板迁入） */}
      <CollapsibleSection
        storagePrefix='analytics-section'
        title={t('dashboard.sections.models')}
        icon={<Brain className='h-4 w-4 text-primary' />}
        storageKey='models'
      >
        <div className='grid gap-4 md:grid-cols-2'>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.requestsCostByModel')}</CardTitle>
              <CardDescription>{t('dashboard.charts.requestsCostByModelDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={modelTimePeriod} onChange={setModelTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <RequestsByModelChart timePeriod={modelTimePeriod} />
            </CardContent>
          </Card>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.tokensByModel')}</CardTitle>
              <CardDescription>{t('dashboard.charts.tokensByModelDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={modelTokensTimePeriod} onChange={setModelTokensTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <TokensByModelChart timePeriod={modelTokensTimePeriod} />
            </CardContent>
          </Card>
        </div>
      </CollapsibleSection>

      {/* API 密钥明细（自仪表板迁入） */}
      <CollapsibleSection
        storagePrefix='analytics-section'
        title={t('dashboard.sections.apiKeys')}
        icon={<Key className='h-4 w-4 text-primary' />}
        storageKey='apiKeys'
      >
        <div className='grid gap-4 md:grid-cols-2'>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.requestsCostByAPIKey')}</CardTitle>
              <CardDescription>{t('dashboard.charts.requestsCostByAPIKeyDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={apiKeyTimePeriod} onChange={setApiKeyTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <RequestsByAPIKeyChart timePeriod={apiKeyTimePeriod} />
            </CardContent>
          </Card>
          <Card className='hover-card'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.tokensByAPIKey')}</CardTitle>
              <CardDescription>{t('dashboard.charts.tokensByAPIKeyDescription')}</CardDescription>
              <CardAction>
                <TimePeriodSelector value={apiKeyTokensTimePeriod} onChange={setApiKeyTokensTimePeriod} />
              </CardAction>
            </CardHeader>
            <CardContent>
              <TokensByAPIKeyChart timePeriod={apiKeyTokensTimePeriod} />
            </CardContent>
          </Card>
        </div>
      </CollapsibleSection>

      {/* 性能分析（自仪表板迁入） */}
      <CollapsibleSection
        storagePrefix='analytics-section'
        title={t('dashboard.sections.performance')}
        icon={<Zap className='h-4 w-4 text-primary' />}
        storageKey='performance'
      >
        <div className='grid gap-4 md:grid-cols-1 lg:grid-cols-7'>
          <Card className='hover-card col-span-1 lg:col-span-4'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.modelPerformance')}</CardTitle>
              <CardDescription>{modelPerformanceDescription}</CardDescription>
            </CardHeader>
            <CardContent>
              <ModelPerformanceStats onTotalRequestsChange={setModelTotalRequests} />
            </CardContent>
          </Card>
          <div className='col-span-1 lg:col-span-3'>
            <FastestModelsCard />
          </div>
        </div>
        <div className='grid gap-4 md:grid-cols-1 lg:grid-cols-7'>
          <Card className='hover-card col-span-1 lg:col-span-4'>
            <CardHeader>
              <CardTitle>{t('dashboard.charts.channelPerformance')}</CardTitle>
              <CardDescription>{channelPerformanceDescription}</CardDescription>
            </CardHeader>
            <CardContent>
              <ChannelPerformanceStats onTotalRequestsChange={setChannelTotalRequests} />
            </CardContent>
          </Card>
          <div className='col-span-1 lg:col-span-3'>
            <FastestChannelsCard />
          </div>
        </div>
      </CollapsibleSection>
    </div>
  );
}
