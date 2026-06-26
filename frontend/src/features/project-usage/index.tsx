import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useProjectUsageOverview } from './data/project-usage';

// ProjectUsage is the registered user's self-service usage dashboard. It shows
// the caller's own project metrics only (non-owners); owners picking a project
// see that project's metrics.
export function ProjectUsage() {
  const { t } = useTranslation();
  const { data, isLoading } = useProjectUsageOverview();

  if (isLoading) {
    return <div className="text-muted-foreground p-6">{t('common.loading')}</div>;
  }

  const stats = [
    { label: t('projectUsage.totalRequests'), value: (data?.totalRequests ?? 0).toLocaleString() },
    { label: t('projectUsage.totalTokens'), value: (data?.totalTokens ?? 0).toLocaleString() },
    {
      label: t('projectUsage.successRate'),
      value: `${((data?.successRate ?? 0) * 100).toFixed(1)}%`,
    },
    { label: t('projectUsage.todayRequests'), value: (data?.todayRequests ?? 0).toLocaleString() },
  ];

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold">{t('projectUsage.title')}</h1>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {stats.map((s) => (
          <Card key={s.label} className="border-0 shadow-sm">
            <CardHeader className="pb-2">
              <CardTitle className="text-muted-foreground text-sm font-medium">{s.label}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-2xl font-bold">{s.value}</p>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
