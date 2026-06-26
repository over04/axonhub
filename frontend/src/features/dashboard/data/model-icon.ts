import * as Icons from '@lobehub/icons';
import providersDataRaw from '@/features/models/data/providers.json';
import { DEVELOPER_ICONS } from '@/features/models/data/constants';

// modelIdToDeveloper mirrors the mapping the "add model" dialog builds, but
// from the LOCAL providers.json (bundled at build time) — no runtime fetch.
// The dashboard availability table only needs modelID -> developer for icon
// fallback, so the local snapshot is enough and avoids a remote request on
// every dashboard load.
const modelIdToDeveloper = new Map<string, string>();
for (const [developerId, provider] of Object.entries(providersDataRaw.providers)) {
  for (const model of provider.models ?? []) {
    modelIdToDeveloper.set(model.id, developerId);
  }
}

// resolveModelIcon returns the lobehub icon name for a model, or null.
// Configured icon wins; otherwise DEVELOPER_ICONS[developer] || developer —
// the same expression used in models-action-dialog when filling a model's
// icon on create.
export function resolveModelIcon(configuredIcon?: string | null, modelId?: string | null): string | null {
  if (configuredIcon && Icons[configuredIcon as keyof typeof Icons]) {
    return configuredIcon;
  }
  if (!modelId) return null;
  const developer = modelIdToDeveloper.get(modelId);
  if (!developer) return null;
  const iconName = DEVELOPER_ICONS[developer] || developer;
  return Icons[iconName as keyof typeof Icons] ? iconName : null;
}
