import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useErrorHandler } from '@/hooks/use-error-handler';

export interface ProjectDailyUsageStat {
  date: string;
  requests: number;
  tokens: number;
}

export interface ProjectUsageOverview {
  totalRequests: number;
  totalTokens: number;
  successRate: number;
  todayRequests: number;
  dailyStats: ProjectDailyUsageStat[];
}

const PROJECT_USAGE_OVERVIEW_QUERY = `
  query ProjectUsageOverview($timeWindow: String) {
    projectUsageOverview(timeWindow: $timeWindow) {
      totalRequests
      totalTokens
      successRate
      todayRequests
      dailyStats {
        date
        requests
        tokens
      }
    }
  }
`;

// useProjectUsageOverview fetches usage metrics + 30-day daily trend scoped to
// the selected project. Registered users see only their own project
// (X-Project-ID + backend ent privacy); the query is enabled only when a
// project is selected.
export function useProjectUsageOverview(timeWindow?: string) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const projectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['projectUsageOverview', projectId, timeWindow],
    enabled: !!projectId,
    queryFn: async () => {
      try {
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;
        const data = await graphqlRequest<{ projectUsageOverview: ProjectUsageOverview }>(
          PROJECT_USAGE_OVERVIEW_QUERY,
          { timeWindow },
          headers
        );
        return data.projectUsageOverview;
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    refetchInterval: 30_000,
  });
}
