import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from '@tanstack/react-router';
import { Users, ChevronRight, TrendingUp } from 'lucide-react';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Header } from '@/components/layout/header';
import { TimePeriodSelector, type TimePeriod } from '@/components/time-period-selector';
import { TokensByUserChart } from './components/tokens-by-user-chart';
import { CollapsibleSection } from './components/collapsible-section';
import { ProjectUsage, ProjectUsageContent } from './components/project-usage';
import { usePermissions } from '@/hooks/usePermissions';
import { useRoutePermissions } from '@/hooks/useRoutePermissions';
import { useSelectedProjectId } from '@/stores/projectStore';

export default function DashboardPage() {
  const { isOwner, isIdentityLoading } = usePermissions();
  // Wait for /me to resolve before branching on isOwner, otherwise an owner
  // hard-refreshing "/" briefly renders the registered-user ProjectUsage
  // (isOwner defaults to false) and flashes before correcting.
  if (isIdentityLoading) {
    return (
      <div className='flex-1 space-y-6 p-8 pt-6'>
        <Header />
        <Skeleton className='h-[400px]' />
      </div>
    );
  }
  // 仪表盘按角色区分，但两种角色都只看「个人用量 + 当前项目统计」；
  // 全局统计（渠道/模型/API Key/性能/模型可用性）统一收归管理组页面
  // /analytics 与 /model-availability。
  if (!isOwner) {
    return <ProjectUsage />;
  }
  return <OwnerDashboard />;
}

// OwnerDashboard 是管理员（owner）的仪表盘：个人用量 + 当前项目统计。
// 全局统计区块已迁至 /analytics（见 features/analytics/index.tsx），
// 模型可用性已迁至 /model-availability。
function OwnerDashboard() {
  const { t } = useTranslation();
  const { isProjectOwner } = useRoutePermissions();
  const projectId = useSelectedProjectId();
  const [userTokensTimePeriod, setUserTokensTimePeriod] = useState<TimePeriod>('day');

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header />

      <div className='space-y-1'>
        <h1 className='text-2xl font-bold tracking-tight'>{t('projectUsage.title')}</h1>
        <p className='text-muted-foreground text-sm'>{t('projectUsage.subtitle')}</p>
      </div>

      {/* 使用详情分析 - 导航卡片（全局统计已迁入 /analytics） */}
      <Link
        to='/analytics'
        className='flex w-full items-center justify-between rounded-lg border bg-card p-4 text-left transition-colors hover:bg-accent/50'
      >
        <div className='flex items-center gap-3'>
          <div className='flex h-8 w-8 items-center justify-center rounded-md bg-primary/10'>
            <TrendingUp className='h-4 w-4 text-primary' />
          </div>
          <div>
            <span className='text-lg font-semibold'>{t('dashboard.sections.analytics')}</span>
            <p className='text-sm text-muted-foreground'>{t('dashboard.sections.analyticsDescription')}</p>
          </div>
        </div>
        <ChevronRight className='h-5 w-5 text-muted-foreground' />
      </Link>

      {/* 当前项目用量 */}
      <ProjectUsageContent />

      {/* 项目内用户分析 - 可折叠（项目级数据，随所选项目变化） */}
      {isProjectOwner && projectId && (
        <CollapsibleSection
          title={t('dashboard.sections.users')}
          icon={<Users className='h-4 w-4 text-primary' />}
          storageKey='users'
        >
          <div className='grid gap-4 md:grid-cols-1 lg:grid-cols-7'>
            <Card className='hover-card col-span-1 lg:col-span-4'>
              <CardHeader>
                <CardTitle>{t('dashboard.charts.tokensByUser')}</CardTitle>
                <CardDescription>{t('dashboard.charts.tokensByUserDescription')}</CardDescription>
                <CardAction>
                  <TimePeriodSelector value={userTokensTimePeriod} onChange={setUserTokensTimePeriod} />
                </CardAction>
              </CardHeader>
              <CardContent>
                <TokensByUserChart timePeriod={userTokensTimePeriod} />
              </CardContent>
            </Card>
          </div>
        </CollapsibleSection>
      )}
    </div>
  );
}
