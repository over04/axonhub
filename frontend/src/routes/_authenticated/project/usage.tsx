import { createFileRoute } from '@tanstack/react-router';
import { ProjectUsage } from '@/features/project-usage';

export const Route = createFileRoute('/_authenticated/project/usage')({
  component: ProjectUsage,
});
