import { createFileRoute, redirect } from '@tanstack/react-router';
import Dashboard from '@/features/dashboard';
import { useAuthStore } from '@/stores/authStore';

export const Route = createFileRoute('/_authenticated/')({
  beforeLoad: () => {
    // 注册用户（非 owner 且无系统级 read_dashboard）不应进入 dashboard——
    // dashboard 的所有 query 会被 ent privacy deny，页面只剩错误提示。
    // 直接重定向到 playground，避免无意义的错误页。
    const user = useAuthStore.getState().auth.user;
    const isOwner = user?.isOwner ?? false;
    const scopes = user?.scopes ?? [];
    const hasDashboard = isOwner || scopes.some((s) => s === 'read_dashboard' || s === '*');
    if (!hasDashboard) {
      throw redirect({ to: '/project/playground', replace: true });
    }
  },
  component: Dashboard,
});
