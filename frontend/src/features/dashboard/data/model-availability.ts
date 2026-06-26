import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { graphqlRequest } from '@/gql/graphql';
import { useErrorHandler } from '@/hooks/use-error-handler';

export interface ModelCapabilities {
  vision?: boolean | null;
  toolCall?: boolean | null;
  reasoning?: boolean | null;
}

export interface ModelHealthPoint {
  timestamp: number;
  totalRequests: number;
  successRequests: number;
  avgLatencyMs?: number | null;
}

export interface ModelAvailabilityItem {
  modelId: string;
  displayName: string;
  icon?: string | null;
  available: boolean;
  successRate?: number | null;
  avgLatencyMs?: number | null;
  capabilities?: ModelCapabilities | null;
  latestStatus: string;
  latestLatencyMs?: number | null;
  healthPoints: ModelHealthPoint[];
}

const MODEL_AVAILABILITY_QUERY = `
  query ModelAvailability {
    modelAvailability {
      modelId
      displayName
      icon
      available
      successRate
      avgLatencyMs
      capabilities {
        vision
        toolCall
        reasoning
      }
      latestStatus
      latestLatencyMs
      healthPoints {
        timestamp
        totalRequests
        successRequests
        avgLatencyMs
      }
    }
  }
`;

// useModelAvailability fetches sanitized, channel-free model availability,
// aggregated globally over a rolling window matching the channel probe
// settings. Output is model-dimension only (no provider/credential/pricing),
// so it is safe for all logged-in users.
export function useModelAvailability() {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();

  return useQuery({
    queryKey: ['modelAvailability'],
    queryFn: async () => {
      try {
        const data = await graphqlRequest<{ modelAvailability: ModelAvailabilityItem[] }>(
          MODEL_AVAILABILITY_QUERY
        );
        return data.modelAvailability;
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
  });
}
