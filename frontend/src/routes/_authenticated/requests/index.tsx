import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import RequestsManagement from '@/features/requests';

/**
 * System-wide request log: lists requests across every project and user.
 * No ProjectGuard and no project header — system-level read_requests is enough.
 */
function ProtectedSystemRequests() {
  return (
    <RouteGuard requiredScopes={['read_requests']} scopeLevel="system">
      <RequestsManagement systemScope />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/requests/')({
  validateSearch: (search: Record<string, unknown>) => search,
  component: ProtectedSystemRequests,
});
