import { useTranslation } from 'react-i18next';
import { Activity, Coins, CheckCircle2, CalendarDays } from 'lucide-react';
import {
  CartesianGrid,
  ResponsiveContainer,
  XAxis,
  YAxis,
  Tooltip,
  Area,
  AreaChart,
  Legend,
} from 'recharts';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Header } from '@/components/layout/header';
import { formatNumber } from '@/utils/format-number';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useProjectUsageOverview } from '../data/project-usage';
import { ModelAvailabilitySection } from './model-availability-section';

// ProjectUsage is the registered user's self-service usage dashboard. It shows
// the caller's own project metrics only (non-owners); owners picking a project
// see that project's metrics. Mirrors the admin dashboard's visual style:
// icon stat cards plus a 30-day requests/tokens trend chart.
export function ProjectUsage() {
  const { t } = useTranslation();
  const projectId = useSelectedProjectId();
  const { data, isLoading } = useProjectUsageOverview();

  // Registered users must have a selected project for the project-scoped
  // overview. Before the project list resolves (or if the user belongs to no
  // project), show an explicit empty state instead of fake all-zero cards —
  // isLoading is false when the query is disabled, so the skeleton alone is
  // not enough.
  if (!projectId) {
    return (
      <div className='flex-1 space-y-6 p-8 pt-6'>
        <Header />
        <div className='text-muted-foreground flex h-64 items-center justify-center text-sm'>
          {t('projectUsage.noProject')}
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className='flex-1 space-y-6 p-8 pt-6'>
        <Header />
        <div className='grid gap-6 md:grid-cols-2 lg:grid-cols-4'>
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className='h-[140px]' />
          ))}
        </div>
        <Skeleton className='h-[400px]' />
      </div>
    );
  }

  const cards = [
    {
      label: t('projectUsage.totalRequests'),
      value: formatNumber(data?.totalRequests ?? 0),
      description: t('projectUsage.totalRequestsDesc'),
      icon: Activity,
      iconClass: 'bg-blue-500/10 text-blue-500',
    },
    {
      label: t('projectUsage.totalTokens'),
      value: formatNumber(data?.totalTokens ?? 0),
      description: t('projectUsage.totalTokensDesc'),
      icon: Coins,
      iconClass: 'bg-emerald-500/10 text-emerald-500',
    },
    {
      label: t('projectUsage.successRate'),
      value: `${((data?.successRate ?? 0) * 100).toFixed(1)}%`,
      description: t('projectUsage.successRateDesc'),
      icon: CheckCircle2,
      iconClass: 'bg-amber-500/10 text-amber-500',
    },
    {
      label: t('projectUsage.todayRequests'),
      value: formatNumber(data?.todayRequests ?? 0),
      description: t('projectUsage.todayRequestsDesc'),
      icon: CalendarDays,
      iconClass: 'bg-violet-500/10 text-violet-500',
    },
  ];

  const chartData =
    (data?.dailyStats ?? []).map((s) => {
      // s.date is 'YYYY-MM-DD' in the configured server timezone; slice to
      // 'MM-DD' directly to avoid re-interpreting it in the browser's local
      // timezone (which can shift the label by a day).
      return {
        name: s.date.slice(5),
        requests: s.requests,
        tokens: s.tokens,
      };
    }) ?? [];

  const requestsMax = Math.max(
    10,
    Math.ceil(Math.max(...chartData.map((d) => d.requests), 0) * 1.1)
  );
  const tokensMax = Math.max(
    1000,
    Math.ceil(Math.max(...chartData.map((d) => d.tokens), 0) * 1.1)
  );

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header />

      <div className='space-y-1'>
        <h1 className='text-2xl font-bold tracking-tight'>{t('projectUsage.title')}</h1>
        <p className='text-muted-foreground text-sm'>{t('projectUsage.subtitle')}</p>
      </div>

      <div className='grid gap-6 md:grid-cols-2 lg:grid-cols-4'>
        {cards.map((c) => {
          const Icon = c.icon;
          return (
            <Card key={c.label} className='hover-card'>
              <CardHeader>
                <div className='flex items-center justify-between'>
                  <CardTitle className='text-muted-foreground text-sm font-medium'>
                    {c.label}
                  </CardTitle>
                  <div
                    className={`flex h-8 w-8 items-center justify-center rounded-md ${c.iconClass}`}
                  >
                    <Icon className='h-4 w-4' />
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <p className='text-3xl font-bold tracking-tight'>{c.value}</p>
                <p className='text-muted-foreground mt-1 text-xs'>{c.description}</p>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <Card className='hover-card'>
        <CardHeader>
          <CardTitle>{t('projectUsage.trendTitle')}</CardTitle>
          <CardDescription>{t('projectUsage.trendDescription')}</CardDescription>
        </CardHeader>
        <CardContent className='pl-2'>
          <ResponsiveContainer width='100%' height={350}>
            <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id='colorRequests' x1='0' y1='0' x2='0' y2='1'>
                  <stop offset='5%' stopColor='var(--chart-1)' stopOpacity={0.3} />
                  <stop offset='95%' stopColor='var(--chart-1)' stopOpacity={0} />
                </linearGradient>
                <linearGradient id='colorTokens' x1='0' y1='0' x2='0' y2='1'>
                  <stop offset='5%' stopColor='var(--chart-2)' stopOpacity={0.3} />
                  <stop offset='95%' stopColor='var(--chart-2)' stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray='3 3' stroke='var(--border)' vertical={false} />
              <XAxis
                dataKey='name'
                stroke='var(--muted-foreground)'
                fontSize={12}
                tickLine
                axisLine
                padding={{ right: 24 }}
              />
              <YAxis
                yAxisId='left'
                stroke='var(--chart-1)'
                fontSize={12}
                domain={[0, requestsMax]}
                tickFormatter={(v) => formatNumber(v)}
                width={40}
                tickMargin={8}
              />
              <YAxis
                yAxisId='tokens'
                orientation='right'
                stroke='var(--chart-2)'
                fontSize={12}
                domain={[0, tokensMax]}
                tickFormatter={(v) => formatNumber(v)}
                width={40}
                tickMargin={8}
              />
              <Tooltip
                formatter={(value: unknown, name: unknown) => [
                  formatNumber(Number(value)),
                  String(name),
                ]}
                contentStyle={{
                  backgroundColor: 'var(--background)',
                  borderColor: 'var(--border)',
                  borderRadius: 'var(--radius)',
                  fontSize: '12px',
                }}
                itemStyle={{ padding: '2px 0' }}
              />
              <Legend verticalAlign='top' height={36} />
              <Area
                yAxisId='left'
                type='monotone'
                dataKey='requests'
                name={t('projectUsage.requests')}
                stroke='var(--chart-1)'
                strokeWidth={2}
                fillOpacity={1}
                fill='url(#colorRequests)'
                dot={false}
                activeDot={{ r: 5 }}
              />
              <Area
                yAxisId='tokens'
                type='monotone'
                dataKey='tokens'
                name={t('projectUsage.tokens')}
                stroke='var(--chart-2)'
                strokeWidth={2}
                fillOpacity={1}
                fill='url(#colorTokens)'
                dot={false}
                activeDot={{ r: 4 }}
              />
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>

      <ModelAvailabilitySection />
    </div>
  );
}
