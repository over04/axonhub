import type { ChannelSettings } from '../data/schema';

export function mergeChannelSettingsForUpdate(
  existing: ChannelSettings | null | undefined,
  patch: Partial<ChannelSettings>
): ChannelSettings {
  const hasOwn = (key: keyof ChannelSettings) => Object.prototype.hasOwnProperty.call(patch, key);
  const pick = <K extends keyof ChannelSettings>(key: K, fallback: ChannelSettings[K]): ChannelSettings[K] => {
    if (!hasOwn(key)) {
      return fallback;
    }
    const value = patch[key];
    return value === undefined ? fallback : (value as ChannelSettings[K]);
  };

  return {
    extraModelPrefix: pick('extraModelPrefix', existing?.extraModelPrefix ?? ''),
    modelMappings: pick('modelMappings', existing?.modelMappings ?? []),
    autoTrimedModelPrefixes: pick('autoTrimedModelPrefixes', existing?.autoTrimedModelPrefixes ?? []),
    hideOriginalModels: pick('hideOriginalModels', existing?.hideOriginalModels ?? false),
    hideMappedModels: pick('hideMappedModels', existing?.hideMappedModels ?? false),
    lowercaseModelId: pick('lowercaseModelId', existing?.lowercaseModelId ?? false),
    paramOverride: pick('paramOverride', existing?.paramOverride ?? ''),
    proxy: pick('proxy', existing?.proxy ?? null),
    transformOptions: pick('transformOptions', existing?.transformOptions ?? undefined),
    passThroughUserAgent: pick('passThroughUserAgent', existing?.passThroughUserAgent ?? null),
    passThroughBody: pick('passThroughBody', existing?.passThroughBody ?? null),
    rateLimit: pick('rateLimit', existing?.rateLimit ?? null),
  };
}
