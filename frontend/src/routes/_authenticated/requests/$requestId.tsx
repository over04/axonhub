import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import RequestDetailGlobalPage from '@/features/requests/components/request-detail-global-page';

function ProtectedRequestDetail() {
  return (
    <RouteGuard requiredScopes={['read_requests']} scopeLevel='system'>
      <RequestDetailGlobalPage />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/requests/$requestId')({
  // projectId is optional: it is supplied by the system-wide request list so the
  // detail page can load project-scoped previews and stored content.
  validateSearch: (search: Record<string, unknown>): Record<string, unknown> & { projectId?: string } => ({
    ...search,
    projectId: typeof search.projectId === 'string' && search.projectId.length > 0 ? search.projectId : undefined,
  }),
  component: ProtectedRequestDetail,
});
