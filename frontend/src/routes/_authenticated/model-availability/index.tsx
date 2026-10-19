import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import ModelAvailabilityPage from '@/features/dashboard/model-availability';

function ProtectedModelAvailability() {
  return (
    <RouteGuard requiredScopes={['read_channels']} scopeLevel="system">
      <ModelAvailabilityPage />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/model-availability/')({
  component: ProtectedModelAvailability,
});
