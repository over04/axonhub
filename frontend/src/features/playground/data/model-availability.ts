import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useErrorHandler } from '@/hooks/use-error-handler';

export interface ModelCapabilities {
  vision?: boolean | null;
  toolCall?: boolean | null;
  reasoning?: boolean | null;
}

export interface ModelAvailabilityItem {
  modelId: string;
  displayName: string;
  available: boolean;
  successRate?: number | null;
  avgLatencyMs?: number | null;
  capabilities?: ModelCapabilities | null;
}

export type ModelAvailabilityScope = 'global' | 'project';

const MODEL_AVAILABILITY_QUERY = `
  query ModelAvailability($scope: String) {
    modelAvailability(scope: $scope) {
      modelId
      displayName
      available
      successRate
      avgLatencyMs
      capabilities {
        vision
        toolCall
        reasoning
      }
    }
  }
`;

// useModelAvailability fetches sanitized, channel-free model availability.
// scope='global' aggregates across the whole system; scope='project' aggregates
// only the caller's own project and requires X-Project-ID so the backend ent
// privacy layer can scope the Request query to that project.
export function useModelAvailability(scope: ModelAvailabilityScope = 'global') {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const projectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['modelAvailability', scope, projectId],
    enabled: scope !== 'project' || !!projectId,
    queryFn: async () => {
      try {
        const headers =
          scope === 'project' && projectId ? { 'X-Project-ID': projectId } : undefined;
        const data = await graphqlRequest<{ modelAvailability: ModelAvailabilityItem[] }>(
          MODEL_AVAILABILITY_QUERY,
          { scope },
          headers
        );
        return data.modelAvailability;
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
  });
}
