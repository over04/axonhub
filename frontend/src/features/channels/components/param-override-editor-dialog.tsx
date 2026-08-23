/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { type DragEvent, type KeyboardEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronUp, Copy, GripVertical, Plus, Search, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ParamRuleCondition = {
  id: string;
  path: string;
  mode: string;
  value_text: string;
  invert: boolean;
  pass_missing_key: boolean;
};

type ParamRule = {
  id: string;
  description: string;
  path: string;
  mode: string;
  from: string;
  to: string;
  value_text: string;
  keep_origin: boolean;
  logic: string;
  conditions: ParamRuleCondition[];
  // match is the array_remove match rule {path, eq}; preserved verbatim so the
  // visual editor never silently drops it (editing happens in JSON mode).
  match: { path?: string; eq?: unknown } | null;
};

export type ParamOverrideEditorDialogProps = {
  open: boolean;
  value: string;
  onOpenChange: (open: boolean) => void;
  onSave: (value: string) => void | Promise<void>;
  saving?: boolean;
};

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const OPERATION_MODE_OPTIONS = [
  { label: 'channels.paramOverride.operations.set', value: 'set' },
  { label: 'channels.paramOverride.operations.delete', value: 'delete' },
  { label: 'channels.paramOverride.operations.append', value: 'append' },
  { label: 'channels.paramOverride.operations.prepend', value: 'prepend' },
  { label: 'channels.paramOverride.operations.copy', value: 'copy' },
  { label: 'channels.paramOverride.operations.move', value: 'move' },
  { label: 'channels.paramOverride.operations.replace', value: 'replace' },
  { label: 'channels.paramOverride.operations.regexReplace', value: 'regex_replace' },
  { label: 'channels.paramOverride.operations.trimPrefix', value: 'trim_prefix' },
  { label: 'channels.paramOverride.operations.trimSuffix', value: 'trim_suffix' },
  { label: 'channels.paramOverride.operations.ensurePrefix', value: 'ensure_prefix' },
  { label: 'channels.paramOverride.operations.ensureSuffix', value: 'ensure_suffix' },
  { label: 'channels.paramOverride.operations.trimSpace', value: 'trim_space' },
  { label: 'channels.paramOverride.operations.toLower', value: 'to_lower' },
  { label: 'channels.paramOverride.operations.toUpper', value: 'to_upper' },
  { label: 'channels.paramOverride.operations.returnError', value: 'return_error' },
  { label: 'channels.paramOverride.operations.pruneObjects', value: 'prune_objects' },
  { label: 'channels.paramOverride.operations.passHeaders', value: 'pass_headers' },
  { label: 'channels.paramOverride.operations.syncFields', value: 'sync_fields' },
  { label: 'channels.paramOverride.operations.setHeader', value: 'set_header' },
  { label: 'channels.paramOverride.operations.deleteHeader', value: 'delete_header' },
  { label: 'channels.paramOverride.operations.copyHeader', value: 'copy_header' },
  { label: 'channels.paramOverride.operations.moveHeader', value: 'move_header' },
  { label: 'channels.paramOverride.operations.arrayRemove', value: 'array_remove' },
];

const OPERATION_MODE_LABEL_MAP = OPERATION_MODE_OPTIONS.reduce<Record<string, string>>((acc, item) => {
  acc[item.value] = item.label;
  return acc;
}, {});

const CONDITION_MODE_OPTIONS = [
  { label: 'channels.paramOverride.conditionModes.full', value: 'full' },
  { label: 'channels.paramOverride.conditionModes.prefix', value: 'prefix' },
  { label: 'channels.paramOverride.conditionModes.suffix', value: 'suffix' },
  { label: 'channels.paramOverride.conditionModes.contains', value: 'contains' },
  { label: 'channels.paramOverride.conditionModes.gt', value: 'gt' },
  { label: 'channels.paramOverride.conditionModes.gte', value: 'gte' },
  { label: 'channels.paramOverride.conditionModes.lt', value: 'lt' },
  { label: 'channels.paramOverride.conditionModes.lte', value: 'lte' },
];

const CONDITION_MODE_VALUES = new Set(CONDITION_MODE_OPTIONS.map((o) => o.value));

const MODE_META: Record<
  string,
  {
    path?: boolean;
    pathOptional?: boolean;
    value?: boolean;
    from?: boolean;
    to?: boolean;
    keepOrigin?: boolean;
    pathAlias?: boolean;
  }
> = {
  delete: { path: true },
  set: { path: true, value: true, keepOrigin: true },
  append: { path: true, value: true, keepOrigin: true },
  prepend: { path: true, value: true, keepOrigin: true },
  copy: { from: true, to: true },
  move: { from: true, to: true },
  replace: { path: true, from: true, to: false },
  regex_replace: { path: true, from: true, to: false },
  trim_prefix: { path: true, value: true },
  trim_suffix: { path: true, value: true },
  ensure_prefix: { path: true, value: true },
  ensure_suffix: { path: true, value: true },
  trim_space: { path: true },
  to_lower: { path: true },
  to_upper: { path: true },
  return_error: { value: true },
  prune_objects: { pathOptional: true, value: true },
  pass_headers: { value: true, keepOrigin: true },
  sync_fields: { from: true, to: true },
  set_header: { path: true, value: true, keepOrigin: true },
  delete_header: { path: true },
  copy_header: { from: true, to: true, keepOrigin: true, pathAlias: true },
  move_header: { from: true, to: true, keepOrigin: true, pathAlias: true },
};

const VALUE_REQUIRED_MODES = new Set([
  'trim_prefix',
  'trim_suffix',
  'ensure_prefix',
  'ensure_suffix',
  'set_header',
  'return_error',
  'prune_objects',
  'pass_headers',
]);

const FROM_REQUIRED_MODES = new Set(['copy', 'move', 'replace', 'regex_replace', 'copy_header', 'move_header', 'sync_fields']);

const TO_REQUIRED_MODES = new Set(['copy', 'move', 'copy_header', 'move_header', 'sync_fields']);

const SYNC_TARGET_TYPE_OPTIONS = [
  { label: 'channels.paramOverride.syncTargetTypes.json', value: 'json' },
  { label: 'channels.paramOverride.syncTargetTypes.header', value: 'header' },
];

// Templates

const OPERATION_TEMPLATE = {
  operations: [
    {
      description: 'Set default temperature for openai/* models.',
      path: 'temperature',
      mode: 'set',
      value: 0.7,
      conditions: [{ path: 'model', mode: 'prefix', value: 'openai/' }],
      logic: 'AND',
    },
  ],
};

const HEADER_PASSTHROUGH_TEMPLATE = {
  operations: [
    {
      description: 'Pass through X-Request-Id header to upstream.',
      mode: 'pass_headers',
      value: ['X-Request-Id'],
      keep_origin: true,
    },
  ],
};

const GEMINI_IMAGE_4K_TEMPLATE = {
  operations: [
    {
      description: 'Set imageSize to 4K when model contains gemini/image and ends with 4k.',
      mode: 'set',
      path: 'generationConfig.imageConfig.imageSize',
      value: '4K',
      conditions: [
        { path: 'original_model', mode: 'contains', value: 'gemini' },
        { path: 'original_model', mode: 'contains', value: 'image' },
        { path: 'original_model', mode: 'suffix', value: '4k' },
      ],
      logic: 'AND',
    },
  ],
};

const CODEX_CLI_HEADER_PASSTHROUGH_HEADERS = ['Originator', 'Session_id', 'User-Agent', 'X-Codex-Beta-Features', 'X-Codex-Turn-Metadata'];

const CLAUDE_CLI_HEADER_PASSTHROUGH_HEADERS = [
  'X-Stainless-Arch',
  'X-Stainless-Lang',
  'X-Stainless-Os',
  'X-Stainless-Package-Version',
  'X-Stainless-Retry-Count',
  'X-Stainless-Runtime',
  'X-Stainless-Runtime-Version',
  'X-Stainless-Timeout',
  'User-Agent',
  'X-App',
  'Anthropic-Beta',
  'Anthropic-Dangerous-Direct-Browser-Access',
  'Anthropic-Version',
];

const buildPassHeadersTemplate = (headers: string[]) => ({
  operations: [{ mode: 'pass_headers', value: [...headers], keep_origin: true }],
});

const CODEX_CLI_HEADER_PASSTHROUGH_TEMPLATE = buildPassHeadersTemplate(CODEX_CLI_HEADER_PASSTHROUGH_HEADERS);
const CLAUDE_CLI_HEADER_PASSTHROUGH_TEMPLATE = buildPassHeadersTemplate(CLAUDE_CLI_HEADER_PASSTHROUGH_HEADERS);

const AWS_BEDROCK_ANTHROPIC_COMPAT_TEMPLATE = {
  operations: [
    {
      description: 'Normalize anthropic-beta header tokens for Bedrock compatibility.',
      mode: 'set_header',
      path: 'anthropic-beta',
      value: {
        'advanced-tool-use-2025-11-20': 'tool-search-tool-2025-10-19',
        bash_20241022: null,
        bash_20250124: null,
        'code-execution-2025-08-25': null,
        'compact-2026-01-12': 'compact-2026-01-12',
        'computer-use-2025-01-24': 'computer-use-2025-01-24',
        'computer-use-2025-11-24': 'computer-use-2025-11-24',
        'context-1m-2025-08-07': 'context-1m-2025-08-07',
        'context-management-2025-06-27': 'context-management-2025-06-27',
        'effort-2025-11-24': null,
        'fast-mode-2026-02-01': null,
        'files-api-2025-04-14': null,
        'fine-grained-tool-streaming-2025-05-14': null,
        'interleaved-thinking-2025-05-14': 'interleaved-thinking-2025-05-14',
        'mcp-client-2025-11-20': null,
        'mcp-client-2025-04-04': null,
        'mcp-servers-2025-12-04': null,
        'output-128k-2025-02-19': null,
        'structured-output-2024-03-01': null,
        'prompt-caching-scope-2026-01-05': null,
        'skills-2025-10-02': null,
        'structured-outputs-2025-11-13': null,
        text_editor_20241022: null,
        text_editor_20250124: null,
        'token-efficient-tools-2025-02-19': null,
        'tool-search-tool-2025-10-19': 'tool-search-tool-2025-10-19',
        'web-fetch-2025-09-10': null,
        'web-search-2025-03-05': null,
        'oauth-2025-04-20': null,
      },
    },
    {
      description: 'Remove all tools[*].custom.input_examples before upstream relay.',
      mode: 'delete',
      path: 'tools.*.custom.input_examples',
    },
  ],
};

type TemplatePresetConfig = {
  label: string;
  payload: Record<string, unknown>;
};

const TEMPLATE_PRESET_CONFIG: Record<string, TemplatePresetConfig> = {
  operations_default: {
    label: 'channels.paramOverride.templates.operationsDefault',
    payload: OPERATION_TEMPLATE,
  },
  pass_headers_auth: {
    label: 'channels.paramOverride.templates.headerPassthrough',
    payload: HEADER_PASSTHROUGH_TEMPLATE,
  },
  gemini_image_4k: {
    label: 'channels.paramOverride.templates.geminiImage4k',
    payload: GEMINI_IMAGE_4K_TEMPLATE,
  },
  claude_cli_headers_passthrough: {
    label: 'channels.paramOverride.templates.claudeCliHeadersPassthrough',
    payload: CLAUDE_CLI_HEADER_PASSTHROUGH_TEMPLATE,
  },
  codex_cli_headers_passthrough: {
    label: 'channels.paramOverride.templates.codexCliHeadersPassthrough',
    payload: CODEX_CLI_HEADER_PASSTHROUGH_TEMPLATE,
  },
  aws_bedrock_anthropic_beta_override: {
    label: 'channels.paramOverride.templates.awsBedrockAnthropicCompat',
    payload: AWS_BEDROCK_ANTHROPIC_COMPAT_TEMPLATE,
  },
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

let localIdSeed = 0;
const nextLocalId = () => `po_${Date.now()}_${localIdSeed++}`;

const toValueText = (value: unknown): string => {
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

const parseLooseValue = (valueText: string): unknown => {
  const raw = String(valueText ?? '').trim();
  if (raw === '') return '';
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
};

const verifyJSON = (text: string): boolean => {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
};

const parseOperationsDocument = (text: string, t: (key: string, options?: Record<string, unknown>) => string): Record<string, unknown> => {
  const parsed = JSON.parse(text) as unknown;
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(t('channels.paramOverride.errors.mustBeObject'));
  }

  const document = parsed as Record<string, unknown>;
  if (!Array.isArray(document.operations)) {
    throw new Error(t('channels.paramOverride.errors.operationsRequired'));
  }

  for (let i = 0; i < document.operations.length; i++) {
    const operation = document.operations[i];
    if (!operation || typeof operation !== 'object' || Array.isArray(operation)) {
      throw new Error(t('channels.paramOverride.errors.ruleMustBeObject', { line: i + 1 }));
    }
    const mode = (operation as Record<string, unknown>).mode;
    if (typeof mode !== 'string' || mode.trim() === '') {
      throw new Error(t('channels.paramOverride.errors.ruleMissingMode', { line: i + 1 }));
    }
  }

  return document;
};

const normalizeCondition = (condition: Record<string, unknown> = {}): ParamRuleCondition => ({
  id: nextLocalId(),
  path: typeof condition.path === 'string' ? condition.path : '',
  mode: CONDITION_MODE_VALUES.has(condition.mode as string) ? (condition.mode as string) : 'full',
  value_text: toValueText(condition.value),
  invert: condition.invert === true,
  pass_missing_key: condition.pass_missing_key === true,
});

const createDefaultCondition = (): ParamRuleCondition => normalizeCondition({});

const normalizeOperation = (operation: Record<string, unknown> = {}): ParamRule => ({
  id: nextLocalId(),
  description: typeof operation.description === 'string' ? operation.description : '',
  path: typeof operation.path === 'string' ? operation.path : '',
  // Preserve unknown modes verbatim (e.g. array_remove) instead of silently
  // rewriting them to 'set' — a silent downgrade would corrupt stored configs.
  mode: typeof operation.mode === 'string' && operation.mode !== '' ? operation.mode : 'set',
  value_text: toValueText(operation.value),
  keep_origin: operation.keep_origin === true,
  from: typeof operation.from === 'string' ? operation.from : '',
  to: typeof operation.to === 'string' ? operation.to : '',
  logic: String(operation.logic || 'OR').toUpperCase() === 'AND' ? 'AND' : 'OR',
  conditions: Array.isArray(operation.conditions) ? (operation.conditions as Record<string, unknown>[]).map(normalizeCondition) : [],
  match:
    operation.match && typeof operation.match === 'object'
      ? {
          path:
            typeof (operation.match as Record<string, unknown>).path === 'string'
              ? String((operation.match as Record<string, unknown>).path)
              : undefined,
          eq: (operation.match as Record<string, unknown>).eq,
        }
      : null,
});

const createDefaultOperation = (): ParamRule => normalizeOperation({ mode: 'set' });

const reorderOperations = (ops: ParamRule[], sourceId: string, targetId: string, position: 'before' | 'after' = 'before'): ParamRule[] => {
  if (!sourceId || !targetId || sourceId === targetId) return ops;
  const srcIdx = ops.findIndex((o) => o.id === sourceId);
  if (srcIdx < 0) return ops;
  const next = [...ops];
  const [moved] = next.splice(srcIdx, 1);
  let insertIdx = next.findIndex((o) => o.id === targetId);
  if (insertIdx < 0) return ops;
  if (position === 'after') insertIdx += 1;
  next.splice(insertIdx, 0, moved);
  return next;
};

const isOperationBlank = (operation: ParamRule): boolean => {
  const hasCondition = operation.conditions.some(
    (c) => c.path.trim() || c.value_text.trim() || c.mode !== 'full' || c.invert || c.pass_missing_key
  );
  return (
    operation.mode === 'set' &&
    !operation.path.trim() &&
    !operation.from.trim() &&
    !operation.to.trim() &&
    operation.value_text.trim() === '' &&
    !operation.keep_origin &&
    !hasCondition
  );
};

const getOperationSummary = (operation: ParamRule, index: number, t: (key: string) => string): string => {
  const mode = operation.mode || 'set';
  const modeLabel = OPERATION_MODE_LABEL_MAP[mode] || mode;
  if (mode === 'sync_fields') {
    const from = operation.from.trim();
    const to = operation.to.trim();
    return `${index + 1}. ${t(modeLabel)} · ${from || to || '-'}`;
  }
  const path = operation.path.trim();
  const from = operation.from.trim();
  const to = operation.to.trim();
  return `${index + 1}. ${t(modeLabel)} · ${path || from || to || '-'}`;
};

const getModeTagTailwind = (mode: string): string => {
  if (mode.includes('header')) return 'bg-cyan-500/15 text-cyan-700 dark:text-cyan-300 border-cyan-500/20';
  if (mode.includes('replace') || mode.includes('trim'))
    return 'bg-violet-500/15 text-violet-700 dark:text-violet-300 border-violet-500/20';
  if (mode.includes('copy') || mode.includes('move')) return 'bg-blue-500/15 text-blue-700 dark:text-blue-300 border-blue-500/20';
  if (mode.includes('error') || mode.includes('prune')) return 'bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/20';
  if (mode.includes('sync')) return 'bg-green-500/15 text-green-700 dark:text-green-300 border-green-500/20';
  return 'bg-muted text-muted-foreground';
};

const getModePathLabel = (mode: string): string => {
  if (mode === 'set_header' || mode === 'delete_header') return 'channels.paramOverride.labels.headerName';
  if (mode === 'prune_objects') return 'channels.paramOverride.labels.targetPathOptional';
  return 'channels.paramOverride.labels.targetFieldPath';
};

const getModePathPlaceholder = (mode: string): string => {
  if (mode === 'set_header') return 'Authorization';
  if (mode === 'delete_header') return 'X-Debug-Mode';
  if (mode === 'prune_objects') return 'messages';
  return 'temperature';
};

const getModeFromLabel = (mode: string): string => {
  if (mode === 'replace') return 'channels.paramOverride.labels.matchText';
  if (mode === 'regex_replace') return 'channels.paramOverride.labels.regexPattern';
  if (mode === 'copy_header' || mode === 'move_header') return 'channels.paramOverride.labels.sourceHeader';
  return 'channels.paramOverride.labels.sourceField';
};

const getModeFromPlaceholder = (mode: string): string => {
  if (mode === 'replace') return 'openai/';
  if (mode === 'regex_replace') return '^gpt-';
  if (mode === 'copy_header' || mode === 'move_header') return 'Authorization';
  return 'model';
};

const getModeToLabel = (mode: string): string => {
  if (mode === 'replace' || mode === 'regex_replace') return 'channels.paramOverride.labels.replaceWith';
  if (mode === 'copy_header' || mode === 'move_header') return 'channels.paramOverride.labels.targetHeader';
  return 'channels.paramOverride.labels.targetField';
};

const getModeToPlaceholder = (mode: string): string => {
  if (mode === 'replace') return '(leave empty to delete)';
  if (mode === 'regex_replace') return 'openai/gpt-';
  if (mode === 'copy_header' || mode === 'move_header') return 'X-Upstream-Auth';
  return 'original_model';
};

const getModeValueLabel = (mode: string): string => {
  if (mode === 'set_header') return 'channels.paramOverride.labels.headerValue';
  if (mode === 'pass_headers') return 'channels.paramOverride.labels.passThroughHeaders';
  if (mode === 'trim_prefix' || mode === 'trim_suffix' || mode === 'ensure_prefix' || mode === 'ensure_suffix')
    return 'channels.paramOverride.labels.prefixSuffixText';
  if (mode === 'prune_objects') return 'channels.paramOverride.labels.pruneRule';
  return 'channels.paramOverride.labels.value';
};

const getModeValuePlaceholder = (mode: string): string => {
  if (mode === 'set_header') return 'Bearer sk-xxx';
  if (mode === 'pass_headers') return 'Authorization, X-Request-Id';
  if (mode === 'trim_prefix' || mode === 'trim_suffix' || mode === 'ensure_prefix' || mode === 'ensure_suffix') return 'openai/';
  if (mode === 'prune_objects') return '{"type":"redacted_thinking"}';
  return '0.7';
};

const parseSyncTargetSpec = (spec: string): { type: string; key: string } => {
  const raw = String(spec ?? '').trim();
  if (!raw) return { type: 'json', key: '' };
  const idx = raw.indexOf(':');
  if (idx < 0) return { type: 'json', key: raw };
  const prefix = raw.slice(0, idx).trim().toLowerCase();
  const key = raw.slice(idx + 1).trim();
  return prefix === 'header' ? { type: 'header', key } : { type: 'json', key };
};

const buildSyncTargetSpec = (type: string, key: string): string => {
  const normalizedType = type === 'header' ? 'header' : 'json';
  const normalizedKey = String(key ?? '').trim();
  if (!normalizedKey) return '';
  return `${normalizedType}:${normalizedKey}`;
};

// return_error helpers

type ReturnErrorDraft = {
  message: string;
  statusCode: number;
  code: string;
  type: string;
  skipRetry: boolean;
  simpleMode: boolean;
};

const parseReturnErrorDraft = (valueText: string): ReturnErrorDraft => {
  const defaults: ReturnErrorDraft = {
    message: '',
    statusCode: 400,
    code: '',
    type: '',
    skipRetry: true,
    simpleMode: true,
  };
  const raw = String(valueText ?? '').trim();
  if (!raw) return defaults;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const statusRaw = parsed.status_code !== undefined ? parsed.status_code : parsed.status;
      const statusValue = Number(statusRaw);
      return {
        ...defaults,
        message: String((parsed.message as string) || (parsed.msg as string) || '').trim(),
        statusCode: Number.isInteger(statusValue) && statusValue >= 100 && statusValue <= 599 ? statusValue : 400,
        code: String((parsed.code as string) || '').trim(),
        type: String((parsed.type as string) || '').trim(),
        skipRetry: parsed.skip_retry !== false,
        simpleMode: false,
      };
    }
  } catch {
    /* treat as plain text */
  }
  return { ...defaults, message: raw, simpleMode: true };
};

const buildReturnErrorValueText = (draft: Partial<ReturnErrorDraft>): string => {
  const message = String(draft.message || '').trim();
  if (draft.simpleMode) return message;
  const statusCode = Number(draft.statusCode);
  const payload: Record<string, unknown> = {
    message,
    status_code: Number.isInteger(statusCode) && statusCode >= 100 && statusCode <= 599 ? statusCode : 400,
  };
  const code = String(draft.code || '').trim();
  const type = String(draft.type || '').trim();
  if (code) payload.code = code;
  if (type) payload.type = type;
  if (draft.skipRetry === false) payload.skip_retry = false;
  return JSON.stringify(payload);
};

// prune_objects helpers

type PruneRule = {
  id: string;
  path: string;
  mode: string;
  value_text: string;
  invert: boolean;
  pass_missing_key: boolean;
};

type PruneObjectsDraft = {
  simpleMode: boolean;
  typeText: string;
  logic: string;
  recursive: boolean;
  rules: PruneRule[];
};

const normalizePruneRule = (rule: Record<string, unknown> = {}): PruneRule => ({
  id: nextLocalId(),
  path: typeof rule.path === 'string' ? rule.path : '',
  mode: CONDITION_MODE_VALUES.has(rule.mode as string) ? (rule.mode as string) : 'full',
  value_text: toValueText(rule.value),
  invert: rule.invert === true,
  pass_missing_key: rule.pass_missing_key === true,
});

const parsePruneObjectsDraft = (valueText: string): PruneObjectsDraft => {
  const defaults: PruneObjectsDraft = {
    simpleMode: true,
    typeText: '',
    logic: 'AND',
    recursive: true,
    rules: [],
  };
  const raw = String(valueText ?? '').trim();
  if (!raw) return defaults;
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed === 'string') return { ...defaults, typeText: parsed.trim() };
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const rules: PruneRule[] = [];
      if (parsed.where && typeof parsed.where === 'object' && !Array.isArray(parsed.where)) {
        for (const [path, value] of Object.entries(parsed.where as Record<string, unknown>)) {
          rules.push(normalizePruneRule({ path, mode: 'full', value }));
        }
      }
      if (Array.isArray(parsed.conditions)) {
        for (const item of parsed.conditions) {
          if (item && typeof item === 'object') rules.push(normalizePruneRule(item));
        }
      } else if (parsed.conditions && typeof parsed.conditions === 'object' && !Array.isArray(parsed.conditions)) {
        for (const [path, value] of Object.entries(parsed.conditions as Record<string, unknown>)) {
          rules.push(normalizePruneRule({ path, mode: 'full', value }));
        }
      }
      const typeText = parsed.type === undefined ? '' : String(parsed.type).trim();
      const logic = String(parsed.logic || 'AND').toUpperCase() === 'OR' ? 'OR' : 'AND';
      const recursive = parsed.recursive !== false;
      const hasAdvancedFields =
        parsed.logic !== undefined || parsed.recursive !== undefined || parsed.where !== undefined || parsed.conditions !== undefined;
      return {
        ...defaults,
        simpleMode: !hasAdvancedFields,
        typeText,
        logic,
        recursive,
        rules,
      };
    }
    return { ...defaults, typeText: String(parsed ?? '').trim() };
  } catch {
    return { ...defaults, typeText: raw };
  }
};

const buildPruneObjectsValueText = (draft: PruneObjectsDraft): string => {
  const typeText = String(draft.typeText || '').trim();
  if (draft.simpleMode) return typeText;
  const payload: Record<string, unknown> = {};
  if (typeText) payload.type = typeText;
  if (String(draft.logic || 'AND').toUpperCase() === 'OR') payload.logic = 'OR';
  if (draft.recursive === false) payload.recursive = false;
  const conditions = (draft.rules || [])
    .filter((rule) => String(rule.path || '').trim())
    .map((rule) => {
      const conditionPayload: Record<string, unknown> = {
        path: String(rule.path || '').trim(),
        mode: CONDITION_MODE_VALUES.has(rule.mode) ? rule.mode : 'full',
      };
      const valueRaw = String(rule.value_text || '').trim();
      if (valueRaw !== '') conditionPayload.value = parseLooseValue(valueRaw);
      if (rule.invert) conditionPayload.invert = true;
      if (rule.pass_missing_key) conditionPayload.pass_missing_key = true;
      return conditionPayload;
    });
  if (conditions.length > 0) payload.conditions = conditions;
  if (!payload.type && !payload.conditions) return JSON.stringify({ logic: 'AND' });
  return JSON.stringify(payload);
};

// pass_headers helpers

const parsePassHeaderNames = (rawValue: unknown): string[] => {
  if (Array.isArray(rawValue)) return rawValue.map((i) => String(i ?? '').trim()).filter(Boolean);
  if (rawValue && typeof rawValue === 'object') {
    const obj = rawValue as Record<string, unknown>;
    if (Array.isArray(obj.headers)) return obj.headers.map((i) => String(i ?? '').trim()).filter(Boolean);
    if (obj.header !== undefined) {
      const single = String(obj.header ?? '').trim();
      return single ? [single] : [];
    }
    return [];
  }
  if (typeof rawValue === 'string')
    return rawValue
      .split(',')
      .map((i) => i.trim())
      .filter(Boolean);
  return [];
};

// Condition payload builder
const buildConditionPayload = (condition: ParamRuleCondition): Record<string, unknown> | null => {
  const path = condition.path.trim();
  if (!path) return null;
  const payload: Record<string, unknown> = {
    path,
    mode: condition.mode || 'full',
    value: parseLooseValue(condition.value_text),
  };
  if (condition.invert) payload.invert = true;
  if (condition.pass_missing_key) payload.pass_missing_key = true;
  return payload;
};

// Validation

const validateOperations = (operations: ParamRule[], t: (key: string, options?: Record<string, unknown>) => string): string => {
  for (let i = 0; i < operations.length; i++) {
    const op = operations[i];
    const mode = op.mode || 'set';
    const meta = MODE_META[mode] || MODE_META.set;
    const line = i + 1;
    const pathValue = op.path.trim();
    const fromValue = op.from.trim();
    const toValue = op.to.trim();

    if (meta.path && !pathValue) return t('channels.paramOverride.errors.ruleMissingTargetPath', { line });
    if (FROM_REQUIRED_MODES.has(mode) && !fromValue) {
      if (!(meta.pathAlias && pathValue)) return t('channels.paramOverride.errors.ruleMissingSourceField', { line });
    }
    if (TO_REQUIRED_MODES.has(mode) && !toValue) {
      if (!(meta.pathAlias && pathValue)) return t('channels.paramOverride.errors.ruleMissingTargetField', { line });
    }
    if (VALUE_REQUIRED_MODES.has(mode) && op.value_text.trim() === '') return t('channels.paramOverride.errors.ruleMissingValue', { line });

    if (mode === 'return_error') {
      const raw = op.value_text.trim();
      if (!raw) return t('channels.paramOverride.errors.ruleMissingValue', { line });
      try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          if (!String((parsed as Record<string, unknown>).message || '').trim())
            return t('channels.paramOverride.errors.returnErrorMessageRequired', {
              line,
            });
        }
      } catch {
        /* plain string is allowed */
      }
    }

    if (mode === 'prune_objects') {
      const raw = op.value_text.trim();
      if (!raw) return t('channels.paramOverride.errors.pruneObjectsConditionsRequired', { line });
    }

    if (mode === 'pass_headers') {
      const raw = op.value_text.trim();
      if (!raw) return t('channels.paramOverride.errors.passHeadersRequired', { line });
      const parsed = parseLooseValue(raw);
      const headers = parsePassHeaderNames(parsed);
      if (headers.length === 0) return t('channels.paramOverride.errors.passHeadersInvalid', { line });
    }
  }
  return '';
};

// Parse initial state

type EditorState = {
  editMode: 'visual' | 'json';
  operations: ParamRule[];
  jsonText: string;
  jsonError: string;
};

const parseInitialState = (rawValue: string, t: (key: string) => string): EditorState => {
  const text = typeof rawValue === 'string' ? rawValue : '';
  const trimmed = text.trim();
  if (!trimmed) {
    return {
      editMode: 'visual',
      operations: [createDefaultOperation()],
      jsonText: '',
      jsonError: '',
    };
  }

  if (!verifyJSON(trimmed)) {
    return {
      editMode: 'json',
      operations: [createDefaultOperation()],
      jsonText: text,
      jsonError: t('channels.paramOverride.errors.jsonFormat'),
    };
  }

  const parsed = JSON.parse(trimmed) as Record<string, unknown>;
  const pretty = JSON.stringify(parsed, null, 2);

  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && Array.isArray(parsed.operations)) {
    return {
      editMode: 'visual',
      operations:
        (parsed.operations as Record<string, unknown>[]).length > 0
          ? (parsed.operations as Record<string, unknown>[]).map(normalizeOperation)
          : [createDefaultOperation()],
      jsonText: pretty,
      jsonError: '',
    };
  }

  return {
    editMode: 'json',
    operations: [createDefaultOperation()],
    jsonText: pretty,
    jsonError: t('channels.paramOverride.errors.operationsRequired'),
  };
};

// Build operations JSON

const buildOperationsJson = (
  sourceOperations: ParamRule[],
  options: { validate: boolean },
  t: (key: string, options?: Record<string, unknown>) => string
): string => {
  const filteredOps = sourceOperations.filter((o) => !isOperationBlank(o));
  if (filteredOps.length === 0) return '';

  if (options.validate) {
    const message = validateOperations(filteredOps, t);
    if (message) throw new Error(message);
  }

  const payloadOps = filteredOps.map((operation) => {
    const mode = operation.mode || 'set';
    const meta = MODE_META[mode] || MODE_META.set;
    const descriptionValue = String(operation.description || '').trim();
    const pathValue = operation.path.trim();
    const fromValue = operation.from.trim();
    const toValue = operation.to.trim();
    const payload: Record<string, unknown> = { mode };
    if (descriptionValue) payload.description = descriptionValue;
    if (meta.path) payload.path = pathValue;
    if (meta.pathOptional && pathValue) payload.path = pathValue;
    if (meta.value) payload.value = parseLooseValue(operation.value_text);
    if (meta.keepOrigin && operation.keep_origin) payload.keep_origin = true;
    if (meta.from) payload.from = fromValue;
    if (!meta.to && operation.to.trim()) payload.to = toValue;
    if (meta.to) payload.to = toValue;
    if (meta.pathAlias) {
      if (!payload.from && pathValue) payload.from = pathValue;
      if (!payload.to && pathValue) payload.to = pathValue;
    }
    if (operation.match && (operation.match.path || operation.match.eq !== undefined)) {
      payload.match = operation.match;
    }
    const conditions = operation.conditions.map(buildConditionPayload).filter(Boolean);
    if (conditions.length > 0) {
      payload.conditions = conditions;
      payload.logic = operation.logic === 'AND' ? 'AND' : 'OR';
    }
    return payload;
  });

  return JSON.stringify({ operations: payloadOps }, null, 2);
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ParamOverrideEditorDialog(props: ParamOverrideEditorDialogProps) {
  const { t } = useTranslation();

  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual');
  const [operations, setOperations] = useState<ParamRule[]>([createDefaultOperation()]);
  const [jsonText, setJsonText] = useState('');
  const [jsonError, setJsonError] = useState('');
  const [operationSearch, setOperationSearch] = useState('');
  const [selectedOperationId, setSelectedOperationId] = useState('');
  const [expandedConditions, setExpandedConditions] = useState<Record<string, boolean>>({});
  const [draggedOperationId, setDraggedOperationId] = useState('');
  const [dragOverOperationId, setDragOverOperationId] = useState('');
  const [dragOverPosition, setDragOverPosition] = useState<'before' | 'after'>('before');
  const [templatePresetKey, setTemplatePresetKey] = useState('operations_default');

  // Initialize state when dialog opens
  useEffect(() => {
    if (!props.open) return;
    const state = parseInitialState(props.value, t);
    setEditMode(state.editMode);
    setOperations(state.operations);
    setJsonText(state.jsonText);
    setJsonError(state.jsonError);
    setOperationSearch('');
    setSelectedOperationId(state.operations[0]?.id || '');
    setExpandedConditions({});
    setDraggedOperationId('');
    setDragOverOperationId('');
    setDragOverPosition('before');
    setTemplatePresetKey('operations_default');
  }, [props.open, props.value, t]);

  // Keep selectedOperationId valid
  useEffect(() => {
    if (operations.length === 0) {
      setSelectedOperationId('');
      return;
    }
    if (!operations.some((o) => o.id === selectedOperationId)) {
      setSelectedOperationId(operations[0].id);
    }
  }, [operations, selectedOperationId]);

  // Template preset options filtered by group
  const templatePresetOptions = useMemo(
    () =>
      Object.entries(TEMPLATE_PRESET_CONFIG).map(([value, config]) => ({
        value,
        label: config.label,
      })),
    []
  );

  const filteredOperations = useMemo(() => {
    const keyword = operationSearch.trim().toLowerCase();
    if (!keyword) return operations;
    return operations.filter((op) => {
      const searchableText = [op.description, op.mode, op.path, op.from, op.to, op.value_text].filter(Boolean).join(' ').toLowerCase();
      return searchableText.includes(keyword);
    });
  }, [operationSearch, operations]);

  const selectedOperation = useMemo(() => operations.find((o) => o.id === selectedOperationId), [operations, selectedOperationId]);

  const selectedOperationIndex = useMemo(
    () => operations.findIndex((o) => o.id === selectedOperationId),
    [operations, selectedOperationId]
  );

  const returnErrorDraft = useMemo(() => {
    if (!selectedOperation || selectedOperation.mode !== 'return_error') return null;
    return parseReturnErrorDraft(selectedOperation.value_text);
  }, [selectedOperation]);

  const pruneObjectsDraft = useMemo(() => {
    if (!selectedOperation || selectedOperation.mode !== 'prune_objects') return null;
    return parsePruneObjectsDraft(selectedOperation.value_text);
  }, [selectedOperation]);

  // ---------------------------------------------------------------------------
  // Operations
  // ---------------------------------------------------------------------------

  const updateOperation = useCallback((operationId: string, patch: Partial<ParamRule>) => {
    setOperations((prev) => prev.map((o) => (o.id === operationId ? { ...o, ...patch } : o)));
  }, []);

  const addOperation = useCallback(() => {
    const created = createDefaultOperation();
    setOperations((prev) => [...prev, created]);
    setSelectedOperationId(created.id);
  }, []);

  const duplicateOperation = useCallback((operationId: string) => {
    let insertedId = '';
    setOperations((prev) => {
      const idx = prev.findIndex((o) => o.id === operationId);
      if (idx < 0) return prev;
      const source = prev[idx];
      const cloned = normalizeOperation({
        description: source.description,
        path: source.path,
        mode: source.mode,
        value: parseLooseValue(source.value_text),
        keep_origin: source.keep_origin,
        from: source.from,
        to: source.to,
        logic: source.logic,
        conditions: source.conditions.map((c) => ({
          path: c.path,
          mode: c.mode,
          value: parseLooseValue(c.value_text),
          invert: c.invert,
          pass_missing_key: c.pass_missing_key,
        })),
      });
      insertedId = cloned.id;
      const next = [...prev];
      next.splice(idx + 1, 0, cloned);
      return next;
    });
    if (insertedId) setSelectedOperationId(insertedId);
  }, []);

  const removeOperation = useCallback((operationId: string) => {
    setOperations((prev) => {
      if (prev.length <= 1) return [createDefaultOperation()];
      return prev.filter((o) => o.id !== operationId);
    });
  }, []);

  // Conditions
  const addCondition = useCallback((operationId: string) => {
    const created = createDefaultCondition();
    setOperations((prev) => prev.map((op) => (op.id === operationId ? { ...op, conditions: [...op.conditions, created] } : op)));
    setExpandedConditions((prev) => ({ ...prev, [created.id]: true }));
  }, []);

  const updateCondition = useCallback((operationId: string, conditionId: string, patch: Partial<ParamRuleCondition>) => {
    setOperations((prev) =>
      prev.map((op) =>
        op.id === operationId
          ? {
              ...op,
              conditions: op.conditions.map((c) => (c.id === conditionId ? { ...c, ...patch } : c)),
            }
          : op
      )
    );
  }, []);

  const removeCondition = useCallback((operationId: string, conditionId: string) => {
    setOperations((prev) =>
      prev.map((op) =>
        op.id === operationId
          ? {
              ...op,
              conditions: op.conditions.filter((c) => c.id !== conditionId),
            }
          : op
      )
    );
  }, []);

  // return_error draft
  const updateReturnErrorDraft = useCallback((operationId: string, draftPatch: Partial<ReturnErrorDraft>) => {
    setOperations((prev) =>
      prev.map((op) => {
        if (op.id !== operationId) return op;
        const draft = parseReturnErrorDraft(op.value_text);
        const nextDraft = { ...draft, ...draftPatch };
        return {
          ...op,
          value_text: buildReturnErrorValueText(nextDraft),
        };
      })
    );
  }, []);

  // prune_objects draft
  const updatePruneObjectsDraft = useCallback(
    (operationId: string, updater: Partial<PruneObjectsDraft> | ((draft: PruneObjectsDraft) => PruneObjectsDraft)) => {
      setOperations((prev) =>
        prev.map((op) => {
          if (op.id !== operationId) return op;
          const draft = parsePruneObjectsDraft(op.value_text);
          const nextDraft = typeof updater === 'function' ? updater(draft) : { ...draft, ...updater };
          return {
            ...op,
            value_text: buildPruneObjectsValueText(nextDraft),
          };
        })
      );
    },
    []
  );

  const addPruneRule = useCallback(
    (operationId: string) => {
      updatePruneObjectsDraft(operationId, (draft) => ({
        ...draft,
        simpleMode: false,
        rules: [...draft.rules, normalizePruneRule({})],
      }));
    },
    [updatePruneObjectsDraft]
  );

  const updatePruneRule = useCallback(
    (operationId: string, ruleId: string, patch: Partial<PruneRule>) => {
      updatePruneObjectsDraft(operationId, (draft) => ({
        ...draft,
        rules: draft.rules.map((r) => (r.id === ruleId ? { ...r, ...patch } : r)),
      }));
    },
    [updatePruneObjectsDraft]
  );

  const removePruneRule = useCallback(
    (operationId: string, ruleId: string) => {
      updatePruneObjectsDraft(operationId, (draft) => ({
        ...draft,
        rules: draft.rules.filter((r) => r.id !== ruleId),
      }));
    },
    [updatePruneObjectsDraft]
  );

  // Drag and drop
  const resetDragState = useCallback(() => {
    setDraggedOperationId('');
    setDragOverOperationId('');
    setDragOverPosition('before');
  }, []);

  const handleDragStart = useCallback((event: DragEvent, operationId: string) => {
    setDraggedOperationId(operationId);
    setSelectedOperationId(operationId);
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', operationId);
  }, []);

  const handleDragOver = useCallback(
    (event: DragEvent, operationId: string) => {
      event.preventDefault();
      if (!draggedOperationId || draggedOperationId === operationId) return;
      const rect = event.currentTarget.getBoundingClientRect();
      const position: 'before' | 'after' = event.clientY - rect.top > rect.height / 2 ? 'after' : 'before';
      setDragOverOperationId(operationId);
      setDragOverPosition(position);
      event.dataTransfer.dropEffect = 'move';
    },
    [draggedOperationId]
  );

  const handleDrop = useCallback(
    (event: DragEvent, operationId: string) => {
      event.preventDefault();
      const sourceId = draggedOperationId || event.dataTransfer.getData('text/plain');
      const position = dragOverOperationId === operationId ? dragOverPosition : 'before';
      if (sourceId && operationId && sourceId !== operationId) {
        setOperations((prev) => reorderOperations(prev, sourceId, operationId, position));
        setSelectedOperationId(sourceId);
      }
      resetDragState();
    },
    [draggedOperationId, dragOverOperationId, dragOverPosition, resetDragState]
  );

  // ---------------------------------------------------------------------------
  // Mode switching & templates
  // ---------------------------------------------------------------------------

  const buildVisualJson = useCallback((): string => {
    return buildOperationsJson(operations, { validate: true }, t);
  }, [operations, t]);

  const switchToJsonMode = useCallback(() => {
    if (editMode === 'json') return;
    try {
      setJsonText(buildVisualJson());
      setJsonError('');
    } catch (error) {
      toast.error((error as Error).message);
      setJsonText(buildOperationsJson(operations, { validate: false }, t));
      setJsonError((error as Error).message || t('channels.paramOverride.errors.configuration'));
    }
    setEditMode('json');
  }, [buildVisualJson, editMode, operations, t]);

  const switchToVisualMode = useCallback(() => {
    if (editMode === 'visual') return;
    const trimmed = jsonText.trim();
    if (!trimmed) {
      const fallback = createDefaultOperation();
      setOperations([fallback]);
      setSelectedOperationId(fallback.id);
      setJsonError('');
      setEditMode('visual');
      return;
    }
    let parsed: Record<string, unknown>;
    try {
      parsed = parseOperationsDocument(trimmed, t);
    } catch (error) {
      toast.error((error as Error).message);
      return;
    }
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && Array.isArray(parsed.operations)) {
      const nextOps =
        (parsed.operations as Record<string, unknown>[]).length > 0
          ? (parsed.operations as Record<string, unknown>[]).map(normalizeOperation)
          : [createDefaultOperation()];
      setOperations(nextOps);
      setSelectedOperationId(nextOps[0]?.id || '');
      setJsonError('');
      setEditMode('visual');
      setTemplatePresetKey('operations_default');
      return;
    }
    toast.error(t('channels.paramOverride.errors.operationsRequired'));
  }, [editMode, jsonText, t]);

  const fillTemplate = useCallback(
    (mode: 'fill' | 'append') => {
      const preset = TEMPLATE_PRESET_CONFIG[templatePresetKey] || TEMPLATE_PRESET_CONFIG.operations_default;
      const payload = preset.payload as Record<string, unknown>;

      const operationsPayload = ((payload as Record<string, unknown>).operations || []) as Record<string, unknown>[];

      if (mode === 'append') {
        const appended = operationsPayload.map(normalizeOperation);
        const existing = operations.filter((o) => !isOperationBlank(o));
        const nextOps = [...existing, ...appended];
        setOperations(nextOps.length > 0 ? nextOps : appended);
        setSelectedOperationId(nextOps[0]?.id || appended[0]?.id || '');
        setJsonError('');
        setEditMode('visual');
        setJsonText('');
      } else {
        const nextOps = operationsPayload.map(normalizeOperation);
        const finalOps = nextOps.length > 0 ? nextOps : [createDefaultOperation()];
        setOperations(finalOps);
        setSelectedOperationId(finalOps[0]?.id || '');
        setJsonText(JSON.stringify({ operations: operationsPayload }, null, 2));
        setJsonError('');
        setEditMode('visual');
      }
    },
    [operations, templatePresetKey]
  );

  const resetEditorState = useCallback(() => {
    const fallback = createDefaultOperation();
    setOperations([fallback]);
    setSelectedOperationId(fallback.id);
    setJsonText('');
    setJsonError('');
    setTemplatePresetKey('operations_default');
    setEditMode('visual');
  }, []);

  // JSON mode
  const handleJsonChange = useCallback(
    (nextValue: string) => {
      setJsonText(nextValue);
      const trimmed = nextValue.trim();
      if (!trimmed) {
        setJsonError('');
        return;
      }
      try {
        parseOperationsDocument(trimmed, t);
        setJsonError('');
      } catch (error) {
        setJsonError((error as Error).message || t('channels.paramOverride.errors.jsonFormat'));
      }
    },
    [t]
  );

  const formatJson = useCallback(() => {
    const trimmed = jsonText.trim();
    if (!trimmed) return;
    if (!verifyJSON(trimmed)) {
      toast.error(t('channels.paramOverride.errors.validJsonRequired'));
      return;
    }
    try {
      setJsonText(JSON.stringify(parseOperationsDocument(trimmed, t), null, 2));
    } catch (error) {
      toast.error((error as Error).message);
      return;
    }
    setJsonError('');
  }, [jsonText, t]);

  const visualValidationError = useMemo(() => {
    if (editMode !== 'visual') return '';
    try {
      buildVisualJson();
      return '';
    } catch (error) {
      return (error as Error)?.message || t('channels.paramOverride.errors.configuration');
    }
  }, [buildVisualJson, editMode, t]);

  // Save
  const handleSave = useCallback(async () => {
    try {
      let result = '';
      if (editMode === 'json') {
        const trimmed = jsonText.trim();
        if (trimmed) {
          if (!verifyJSON(trimmed)) throw new Error(t('channels.paramOverride.errors.validJsonRequired'));
          result = JSON.stringify(parseOperationsDocument(trimmed, t), null, 2);
        }
      } else {
        result = buildVisualJson();
      }
      await props.onSave(result);
      props.onOpenChange(false);
    } catch (error) {
      toast.error((error as Error).message);
    }
  }, [buildVisualJson, editMode, jsonText, props, t]);

  // Expand/collapse all conditions
  const expandAllConditions = useCallback(() => {
    if (!selectedOperation) return;
    const map: Record<string, boolean> = {};
    for (const c of selectedOperation.conditions) map[c.id] = true;
    setExpandedConditions((prev) => ({ ...prev, ...map }));
  }, [selectedOperation]);

  const collapseAllConditions = useCallback(() => {
    if (!selectedOperation) return;
    const map: Record<string, boolean> = {};
    for (const c of selectedOperation.conditions) map[c.id] = false;
    setExpandedConditions((prev) => ({ ...prev, ...map }));
  }, [selectedOperation]);

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='flex h-[90vh] max-h-[90vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl'>
        <DialogHeader className='border-b px-6 py-4'>
          <DialogTitle>{t('channels.paramOverride.title')}</DialogTitle>
        </DialogHeader>

        {/* Toolbar */}
        <div className='bg-muted/30 border-b px-4 py-3'>
          <div className='flex flex-wrap items-center gap-2'>
            <span className='text-muted-foreground text-xs font-medium'>{t('channels.paramOverride.labels.mode')}</span>
            <Button type='button' variant={editMode === 'visual' ? 'default' : 'outline'} size='sm' onClick={switchToVisualMode}>
              {t('channels.paramOverride.modes.visual')}
            </Button>
            <Button type='button' variant={editMode === 'json' ? 'default' : 'outline'} size='sm' onClick={switchToJsonMode}>
              {t('channels.paramOverride.modes.jsonText')}
            </Button>

            <div className='bg-border mx-1 h-5 w-px' />

            <span className='text-muted-foreground text-xs font-medium'>{t('channels.paramOverride.labels.template')}</span>
            <Select value={templatePresetKey} onValueChange={(v) => setTemplatePresetKey(v || 'operations_default')}>
              <SelectTrigger className='h-8 w-[220px]'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {templatePresetOptions.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(o.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button type='button' variant='outline' size='sm' onClick={() => fillTemplate('fill')}>
              {t('channels.paramOverride.actions.fillTemplate')}
            </Button>
            <Button type='button' variant='ghost' size='sm' onClick={() => fillTemplate('append')}>
              {t('channels.paramOverride.actions.appendTemplate')}
            </Button>
            <Button type='button' variant='ghost' size='sm' onClick={resetEditorState}>
              {t('channels.paramOverride.actions.reset')}
            </Button>
          </div>
        </div>

        {/* Content */}
        <div className='min-h-0 flex-1 overflow-hidden'>
          {editMode === 'visual' ? (
            <div className='flex h-full flex-col md:flex-row'>
              {/* Left sidebar */}
              <div className='flex h-[220px] w-full flex-shrink-0 flex-col border-b md:h-auto md:w-[280px] md:border-r md:border-b-0'>
                <div className='flex items-center justify-between border-b px-3 py-2'>
                  <div className='flex items-center gap-2'>
                    <span className='text-sm font-medium'>{t('channels.paramOverride.labels.rules')}</span>
                  </div>
                  <Button type='button' variant='ghost' size='sm' onClick={addOperation}>
                    <Plus className='h-4 w-4' />
                  </Button>
                </div>

                <div className='px-3 py-2'>
                  <div className='relative'>
                    <Search className='text-muted-foreground absolute top-2.5 left-2.5 h-3.5 w-3.5' />
                    <Input
                      value={operationSearch}
                      onChange={(e) => setOperationSearch(e.target.value)}
                      placeholder={t('channels.paramOverride.placeholders.searchRules')}
                      className='h-8 pl-8 text-xs'
                    />
                  </div>
                </div>

                <div className='min-h-0 flex-1 overflow-y-auto'>
                  <div className='flex flex-col gap-1 px-3 pb-3'>
                    {filteredOperations.length === 0 ? (
                      <p className='text-muted-foreground py-4 text-center text-xs'>{t('channels.paramOverride.empty.noMatchingRules')}</p>
                    ) : (
                      filteredOperations.map((operation) => {
                        const index = operations.findIndex((o) => o.id === operation.id);
                        const isActive = operation.id === selectedOperationId;
                        const isDragging = operation.id === draggedOperationId;
                        const isDropTarget =
                          operation.id === dragOverOperationId && draggedOperationId !== '' && draggedOperationId !== operation.id;
                        return (
                          <div
                            key={operation.id}
                            role='button'
                            tabIndex={0}
                            draggable={operations.length > 1}
                            onClick={() => setSelectedOperationId(operation.id)}
                            onDragStart={(e) => handleDragStart(e, operation.id)}
                            onDragOver={(e) => handleDragOver(e, operation.id)}
                            onDrop={(e) => handleDrop(e, operation.id)}
                            onDragEnd={resetDragState}
                            onKeyDown={(e: KeyboardEvent) => {
                              if (e.key === 'Enter' || e.key === ' ') {
                                e.preventDefault();
                                setSelectedOperationId(operation.id);
                              }
                            }}
                            className={cn(
                              'cursor-pointer rounded-lg border p-2.5 transition-colors',
                              isActive ? 'border-primary bg-primary/5' : 'hover:bg-muted/50',
                              isDragging && 'opacity-50',
                              isDropTarget && dragOverPosition === 'before' && 'border-t-primary border-t-2',
                              isDropTarget && dragOverPosition === 'after' && 'border-b-primary border-b-2'
                            )}
                          >
                            <div className='flex items-start gap-2'>
                              <GripVertical
                                className={cn(
                                  'text-muted-foreground mt-0.5 h-3.5 w-3.5 flex-shrink-0',
                                  operations.length > 1 ? 'cursor-grab' : 'cursor-default'
                                )}
                              />
                              <div className='min-w-0 flex-1'>
                                <div className='flex items-center justify-between gap-1'>
                                  <span className='text-xs font-semibold'>#{index + 1}</span>
                                  <Badge variant='outline' className='text-[10px]'>
                                    {operation.conditions.length}
                                  </Badge>
                                </div>
                                <p className='text-muted-foreground mt-0.5 line-clamp-1 text-[11px]'>
                                  {getOperationSummary(operation, index, t)}
                                </p>
                                {operation.description.trim() && (
                                  <p className='text-muted-foreground mt-0.5 line-clamp-2 text-[10px]'>{operation.description}</p>
                                )}
                                <span
                                  className={cn(
                                    'mt-1 inline-flex items-center rounded-md border px-1.5 py-0.5 text-[10px] font-medium',
                                    getModeTagTailwind(operation.mode || 'set')
                                  )}
                                >
                                  {t(OPERATION_MODE_LABEL_MAP[operation.mode || 'set'] || operation.mode || 'set')}
                                </span>
                              </div>
                            </div>
                          </div>
                        );
                      })
                    )}
                  </div>
                </div>
              </div>

              {/* Right panel - Rule editor */}
              <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'>
                {selectedOperation ? (
                  <RuleEditor
                    operation={selectedOperation}
                    operationIndex={selectedOperationIndex}
                    operations={operations}
                    returnErrorDraft={returnErrorDraft}
                    pruneObjectsDraft={pruneObjectsDraft}
                    expandedConditions={expandedConditions}
                    setExpandedConditions={setExpandedConditions}
                    updateOperation={updateOperation}
                    duplicateOperation={duplicateOperation}
                    removeOperation={removeOperation}
                    addCondition={addCondition}
                    updateCondition={updateCondition}
                    removeCondition={removeCondition}
                    updateReturnErrorDraft={updateReturnErrorDraft}
                    updatePruneObjectsDraft={updatePruneObjectsDraft}
                    addPruneRule={addPruneRule}
                    updatePruneRule={updatePruneRule}
                    removePruneRule={removePruneRule}
                    expandAllConditions={expandAllConditions}
                    collapseAllConditions={collapseAllConditions}
                  />
                ) : (
                  <div className='flex flex-1 items-center justify-center'>
                    <p className='text-muted-foreground text-sm'>{t('channels.paramOverride.empty.selectRule')}</p>
                  </div>
                )}

                {visualValidationError && (
                  <div className='border-t px-4 py-2'>
                    <p className='text-destructive text-xs'>{visualValidationError}</p>
                  </div>
                )}
              </div>
            </div>
          ) : (
            /* JSON mode */
            <div className='h-full overflow-y-auto p-4'>
              <div className='mb-2 flex items-center gap-2'>
                <Button type='button' variant='outline' size='sm' onClick={formatJson}>
                  {t('channels.paramOverride.actions.format')}
                </Button>
              </div>
              <Textarea
                value={jsonText}
                onChange={(e) => handleJsonChange(e.target.value)}
                placeholder={JSON.stringify(OPERATION_TEMPLATE, null, 2)}
                rows={20}
                className='font-mono text-xs'
              />
              {jsonError && <p className='text-destructive mt-1 text-xs'>{jsonError}</p>}
            </div>
          )}
        </div>

        {/* Footer */}
        <DialogFooter className='border-t px-6 py-4'>
          <Button type='button' variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('common.buttons.cancel')}
          </Button>
          <Button type='button' onClick={handleSave} disabled={props.saving}>
            {props.saving ? t('common.buttons.saving') : t('common.buttons.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// RuleEditor sub-component
// ---------------------------------------------------------------------------

type RuleEditorProps = {
  operation: ParamRule;
  operationIndex: number;
  operations: ParamRule[];
  returnErrorDraft: ReturnErrorDraft | null;
  pruneObjectsDraft: PruneObjectsDraft | null;
  expandedConditions: Record<string, boolean>;
  setExpandedConditions: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  updateOperation: (operationId: string, patch: Partial<ParamRule>) => void;
  duplicateOperation: (operationId: string) => void;
  removeOperation: (operationId: string) => void;
  addCondition: (operationId: string) => void;
  updateCondition: (operationId: string, conditionId: string, patch: Partial<ParamRuleCondition>) => void;
  removeCondition: (operationId: string, conditionId: string) => void;
  updateReturnErrorDraft: (operationId: string, draftPatch: Partial<ReturnErrorDraft>) => void;
  updatePruneObjectsDraft: (
    operationId: string,
    updater: Partial<PruneObjectsDraft> | ((draft: PruneObjectsDraft) => PruneObjectsDraft)
  ) => void;
  addPruneRule: (operationId: string) => void;
  updatePruneRule: (operationId: string, ruleId: string, patch: Partial<PruneRule>) => void;
  removePruneRule: (operationId: string, ruleId: string) => void;
  expandAllConditions: () => void;
  collapseAllConditions: () => void;
};

function RuleEditor(ruleEditorProps: RuleEditorProps) {
  const { t } = useTranslation();
  const operation = ruleEditorProps.operation;
  const mode = operation.mode || 'set';
  const meta = MODE_META[mode] || MODE_META.set;
  const conditions = operation.conditions;
  const syncFromTarget = mode === 'sync_fields' ? parseSyncTargetSpec(operation.from) : null;
  const syncToTarget = mode === 'sync_fields' ? parseSyncTargetSpec(operation.to) : null;

  return (
    <div className='min-h-0 flex-1 overflow-y-auto'>
      <div className='space-y-4 p-4'>
        {/* Header */}
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2'>
            <Badge variant='outline'>#{ruleEditorProps.operationIndex + 1}</Badge>
            <span className='text-muted-foreground line-clamp-1 text-xs'>
              {getOperationSummary(operation, ruleEditorProps.operationIndex, t)}
            </span>
          </div>
          <div className='flex items-center gap-1'>
            <Button type='button' variant='ghost' size='sm' onClick={() => ruleEditorProps.duplicateOperation(operation.id)}>
              <Copy className='mr-1 h-3.5 w-3.5' />
              {t('channels.paramOverride.actions.duplicate')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='text-destructive hover:text-destructive'
              onClick={() => ruleEditorProps.removeOperation(operation.id)}
            >
              <Trash2 className='mr-1 h-3.5 w-3.5' />
              {t('channels.paramOverride.actions.delete')}
            </Button>
          </div>
        </div>

        {/* Operation type + path */}
        <div className='grid gap-3 sm:grid-cols-2'>
          <div className='space-y-1.5'>
            <label className='text-xs font-medium'>{t('channels.paramOverride.labels.operationType')}</label>
            <Select
              value={mode}
              onValueChange={(nextMode) =>
                nextMode !== null &&
                ruleEditorProps.updateOperation(operation.id, {
                  mode: nextMode,
                })
              }
            >
              <SelectTrigger className='h-9'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {OPERATION_MODE_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(o.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          {(meta.path || meta.pathOptional) && (
            <div className='space-y-1.5'>
              <label className='text-xs font-medium'>{t(getModePathLabel(mode))}</label>
              <Input
                value={operation.path}
                onChange={(e) =>
                  ruleEditorProps.updateOperation(operation.id, {
                    path: e.target.value,
                  })
                }
                placeholder={getModePathPlaceholder(mode)}
                className='h-9'
              />
            </div>
          )}
        </div>

        {/* Description */}
        <div className='space-y-1.5'>
          <div className='flex items-center justify-between'>
            <label className='text-xs font-medium'>{t('channels.paramOverride.labels.ruleDescription')}</label>
            <span className='text-muted-foreground text-[10px]'>{operation.description.length}/180</span>
          </div>
          <Input
            value={operation.description}
            onChange={(e) =>
              ruleEditorProps.updateOperation(operation.id, {
                description: e.target.value,
              })
            }
            placeholder={t('channels.paramOverride.placeholders.ruleDescription')}
            maxLength={180}
            className='h-9'
          />
        </div>

        {/* Value section */}
        {meta.value &&
          (mode === 'return_error' && ruleEditorProps.returnErrorDraft ? (
            <ReturnErrorEditor
              operationId={operation.id}
              draft={ruleEditorProps.returnErrorDraft}
              updateDraft={ruleEditorProps.updateReturnErrorDraft}
            />
          ) : mode === 'prune_objects' && ruleEditorProps.pruneObjectsDraft ? (
            <PruneObjectsEditor
              operationId={operation.id}
              draft={ruleEditorProps.pruneObjectsDraft}
              updateDraft={ruleEditorProps.updatePruneObjectsDraft}
              addRule={ruleEditorProps.addPruneRule}
              updateRule={ruleEditorProps.updatePruneRule}
              removeRule={ruleEditorProps.removePruneRule}
            />
          ) : (
            <div className='space-y-1.5'>
              <div className='flex items-center justify-between'>
                <label className='text-xs font-medium'>{t(getModeValueLabel(mode))}</label>
                {operation.value_text.trim().startsWith('{') && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='text-muted-foreground h-auto px-1.5 py-0.5 text-xs'
                    onClick={() => {
                      try {
                        const parsed = JSON.parse(operation.value_text);
                        ruleEditorProps.updateOperation(operation.id, {
                          value_text: JSON.stringify(parsed, null, 2),
                        });
                      } catch (_e) {
                        /* not valid JSON */
                      }
                    }}
                  >
                    {t('channels.paramOverride.actions.format')}
                  </Button>
                )}
              </div>
              <Textarea
                value={operation.value_text}
                onChange={(e) =>
                  ruleEditorProps.updateOperation(operation.id, {
                    value_text: e.target.value,
                  })
                }
                placeholder={getModeValuePlaceholder(mode)}
                rows={3}
                className='max-h-[200px] resize-y overflow-y-auto font-mono text-xs'
              />
            </div>
          ))}

        {/* keep_origin */}
        {meta.keepOrigin && (
          <div className='flex items-center justify-between rounded-lg border px-3 py-2'>
            <p className='text-sm font-medium'>{t('channels.paramOverride.labels.keepOrigin')}</p>
            <Switch
              checked={operation.keep_origin}
              onCheckedChange={(checked) =>
                ruleEditorProps.updateOperation(operation.id, {
                  keep_origin: checked,
                })
              }
            />
          </div>
        )}

        {/* sync_fields */}
        {mode === 'sync_fields' && syncFromTarget && syncToTarget ? (
          <SyncFieldsEditor
            operationId={operation.id}
            syncFromTarget={syncFromTarget}
            syncToTarget={syncToTarget}
            updateOperation={ruleEditorProps.updateOperation}
          />
        ) : (meta.from || meta.to !== undefined) && mode !== 'sync_fields' ? (
          <div className='grid gap-3 sm:grid-cols-2'>
            {(meta.from || meta.to === false) && (
              <div className='space-y-1.5'>
                <label className='text-xs font-medium'>{t(getModeFromLabel(mode))}</label>
                <Input
                  value={operation.from}
                  onChange={(e) =>
                    ruleEditorProps.updateOperation(operation.id, {
                      from: e.target.value,
                    })
                  }
                  placeholder={getModeFromPlaceholder(mode)}
                  className='h-9'
                />
              </div>
            )}
            {(meta.to || meta.to === false) && (
              <div className='space-y-1.5'>
                <label className='text-xs font-medium'>{t(getModeToLabel(mode))}</label>
                <Input
                  value={operation.to}
                  onChange={(e) =>
                    ruleEditorProps.updateOperation(operation.id, {
                      to: e.target.value,
                    })
                  }
                  placeholder={getModeToPlaceholder(mode)}
                  className='h-9'
                />
              </div>
            )}
          </div>
        ) : null}

        {/* Conditions */}
        <div className='rounded-lg border p-3'>
          <div className='mb-2 flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <span className='text-sm font-medium'>{t('channels.paramOverride.labels.conditions')}</span>
              <Select
                value={operation.logic || 'OR'}
                onValueChange={(v) =>
                  v !== null &&
                  ruleEditorProps.updateOperation(operation.id, {
                    logic: v,
                  })
                }
              >
                <SelectTrigger className='h-7 w-[120px] text-xs'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='OR'>{t('channels.paramOverride.logic.matchAny')}</SelectItem>
                    <SelectItem value='AND'>{t('channels.paramOverride.logic.matchAll')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='flex items-center gap-1'>
              {conditions.length > 0 && (
                <>
                  <Button type='button' variant='ghost' size='sm' className='h-7 text-xs' onClick={ruleEditorProps.expandAllConditions}>
                    <ChevronDown className='mr-1 h-3 w-3' />
                    {t('channels.paramOverride.actions.expandAll')}
                  </Button>
                  <Button type='button' variant='ghost' size='sm' className='h-7 text-xs' onClick={ruleEditorProps.collapseAllConditions}>
                    <ChevronUp className='mr-1 h-3 w-3' />
                    {t('channels.paramOverride.actions.collapseAll')}
                  </Button>
                </>
              )}
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-7 text-xs'
                onClick={() => ruleEditorProps.addCondition(operation.id)}
              >
                <Plus className='mr-1 h-3 w-3' />
                {t('channels.paramOverride.actions.addCondition')}
              </Button>
            </div>
          </div>

          {conditions.length === 0 ? (
            <div className='h-1' />
          ) : (
            <div className='space-y-2'>
              {conditions.map((condition, conditionIndex) => (
                <ConditionEditor
                  key={condition.id}
                  condition={condition}
                  conditionIndex={conditionIndex}
                  operationId={operation.id}
                  expanded={ruleEditorProps.expandedConditions[condition.id] ?? false}
                  onExpandedChange={(expanded) =>
                    ruleEditorProps.setExpandedConditions((prev) => ({
                      ...prev,
                      [condition.id]: expanded,
                    }))
                  }
                  updateCondition={ruleEditorProps.updateCondition}
                  removeCondition={ruleEditorProps.removeCondition}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ConditionEditor
// ---------------------------------------------------------------------------

type ConditionEditorProps = {
  condition: ParamRuleCondition;
  conditionIndex: number;
  operationId: string;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  updateCondition: (operationId: string, conditionId: string, patch: Partial<ParamRuleCondition>) => void;
  removeCondition: (operationId: string, conditionId: string) => void;
};

function ConditionEditor(conditionEditorProps: ConditionEditorProps) {
  const { t } = useTranslation();
  const condition = conditionEditorProps.condition;

  return (
    <Collapsible open={conditionEditorProps.expanded} onOpenChange={conditionEditorProps.onExpandedChange}>
      <div className='rounded-md border'>
        <CollapsibleTrigger className='hover:bg-muted/50 flex w-full items-center justify-between px-3 py-2'>
          <div className='flex items-center gap-2'>
            <Badge variant='outline' className='text-[10px]'>
              C{conditionEditorProps.conditionIndex + 1}
            </Badge>
            <span className='text-muted-foreground text-xs'>{condition.path || t('channels.paramOverride.empty.pathNotSet')}</span>
          </div>
          {conditionEditorProps.expanded ? (
            <ChevronUp className='text-muted-foreground h-3.5 w-3.5' />
          ) : (
            <ChevronDown className='text-muted-foreground h-3.5 w-3.5' />
          )}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className='space-y-3 border-t px-3 py-3'>
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-xs'>{t('channels.paramOverride.labels.conditionSettings')}</span>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='text-destructive hover:text-destructive h-7 text-xs'
                onClick={() => conditionEditorProps.removeCondition(conditionEditorProps.operationId, condition.id)}
              >
                <Trash2 className='mr-1 h-3 w-3' />
                {t('channels.paramOverride.actions.deleteCondition')}
              </Button>
            </div>
            <div className='grid gap-2 sm:grid-cols-3'>
              <div className='space-y-1'>
                <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.fieldPath')}</label>
                <Input
                  value={condition.path}
                  onChange={(e) =>
                    conditionEditorProps.updateCondition(conditionEditorProps.operationId, condition.id, { path: e.target.value })
                  }
                  placeholder='model'
                  className='h-8 text-xs'
                />
              </div>
              <div className='space-y-1'>
                <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.matchMode')}</label>
                <Select
                  value={condition.mode}
                  onValueChange={(v) =>
                    v !== null && conditionEditorProps.updateCondition(conditionEditorProps.operationId, condition.id, { mode: v })
                  }
                >
                  <SelectTrigger className='h-8 text-xs'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {CONDITION_MODE_OPTIONS.map((o) => (
                        <SelectItem key={o.value} value={o.value}>
                          {t(o.label)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <div className='space-y-1'>
                <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.matchValue')}</label>
                <Input
                  value={condition.value_text}
                  onChange={(e) =>
                    conditionEditorProps.updateCondition(conditionEditorProps.operationId, condition.id, { value_text: e.target.value })
                  }
                  placeholder='gpt'
                  className='h-8 text-xs'
                />
              </div>
            </div>
            <div className='flex flex-wrap gap-4'>
              <label className='flex items-center gap-2 text-xs'>
                <Switch
                  checked={condition.invert}
                  onCheckedChange={(checked) =>
                    conditionEditorProps.updateCondition(conditionEditorProps.operationId, condition.id, { invert: checked })
                  }
                />
                {t('channels.paramOverride.labels.invertMatch')}
              </label>
              <label className='flex items-center gap-2 text-xs'>
                <Switch
                  checked={condition.pass_missing_key}
                  onCheckedChange={(checked) =>
                    conditionEditorProps.updateCondition(conditionEditorProps.operationId, condition.id, { pass_missing_key: checked })
                  }
                />
                {t('channels.paramOverride.labels.passMissingKey')}
              </label>
            </div>
          </div>
        </CollapsibleContent>
      </div>
    </Collapsible>
  );
}

// ---------------------------------------------------------------------------
// ReturnErrorEditor
// ---------------------------------------------------------------------------

type ReturnErrorEditorProps = {
  operationId: string;
  draft: ReturnErrorDraft;
  updateDraft: (operationId: string, draftPatch: Partial<ReturnErrorDraft>) => void;
};

function ReturnErrorEditor(returnErrorEditorProps: ReturnErrorEditorProps) {
  const { t } = useTranslation();
  const draft = returnErrorEditorProps.draft;

  return (
    <div className='rounded-lg border p-3'>
      <div className='mb-2 flex items-center justify-between'>
        <span className='text-sm font-medium'>{t('channels.paramOverride.returnError.title')}</span>
        <div className='flex items-center gap-1'>
          <span className='text-muted-foreground text-xs'>{t('channels.paramOverride.labels.mode')}</span>
          <Button
            type='button'
            variant={draft.simpleMode ? 'default' : 'outline'}
            size='sm'
            className='h-7 text-xs'
            onClick={() => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { simpleMode: true })}
          >
            {t('channels.paramOverride.modes.simple')}
          </Button>
          <Button
            type='button'
            variant={draft.simpleMode ? 'outline' : 'default'}
            size='sm'
            className='h-7 text-xs'
            onClick={() => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { simpleMode: false })}
          >
            {t('channels.paramOverride.modes.advanced')}
          </Button>
        </div>
      </div>

      <div className='space-y-1.5'>
        <label className='text-xs font-medium'>{t('channels.paramOverride.returnError.message')}</label>
        <Textarea
          value={draft.message}
          onChange={(e) => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { message: e.target.value })}
          placeholder={t('channels.paramOverride.returnError.messagePlaceholder')}
          rows={2}
          className='text-xs'
        />
      </div>

      {draft.simpleMode ? (
        <p className='text-muted-foreground mt-2 text-xs'>{t('channels.paramOverride.returnError.simpleHelp')}</p>
      ) : (
        <>
          <div className='mt-3 grid gap-3 sm:grid-cols-3'>
            <div className='space-y-1'>
              <label className='text-xs font-medium'>{t('channels.paramOverride.returnError.statusCode')}</label>
              <Input
                value={String(draft.statusCode ?? '')}
                onChange={(e) =>
                  returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, {
                    statusCode: parseInt(e.target.value, 10) || 400,
                  })
                }
                placeholder='400'
                className='h-8 text-xs'
              />
            </div>
            <div className='space-y-1'>
              <label className='text-xs font-medium'>{t('channels.paramOverride.returnError.errorCode')}</label>
              <Input
                value={draft.code}
                onChange={(e) => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { code: e.target.value })}
                placeholder='forced_bad_request'
                className='h-8 text-xs'
              />
            </div>
            <div className='space-y-1'>
              <label className='text-xs font-medium'>{t('channels.paramOverride.returnError.errorType')}</label>
              <Input
                value={draft.type}
                onChange={(e) => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { type: e.target.value })}
                placeholder='invalid_request_error'
                className='h-8 text-xs'
              />
            </div>
          </div>
          <div className='mt-2 flex items-center gap-2'>
            <span className='text-muted-foreground text-xs'>{t('channels.paramOverride.returnError.retrySuggestion')}</span>
            <Button
              type='button'
              variant={draft.skipRetry ? 'default' : 'outline'}
              size='sm'
              className='h-7 text-xs'
              onClick={() => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { skipRetry: true })}
            >
              {t('channels.paramOverride.returnError.stopRetry')}
            </Button>
            <Button
              type='button'
              variant={draft.skipRetry ? 'outline' : 'default'}
              size='sm'
              className='h-7 text-xs'
              onClick={() => returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, { skipRetry: false })}
            >
              {t('channels.paramOverride.returnError.allowRetry')}
            </Button>
          </div>
          <div className='mt-2 flex flex-wrap gap-1'>
            {[
              {
                label: 'channels.paramOverride.returnError.presets.badRequest',
                statusCode: 400,
                code: 'invalid_request',
                type: 'invalid_request_error',
              },
              {
                label: 'channels.paramOverride.returnError.presets.unauthorized',
                statusCode: 401,
                code: 'unauthorized',
                type: 'authentication_error',
              },
              {
                label: 'channels.paramOverride.returnError.presets.rateLimited',
                statusCode: 429,
                code: 'rate_limited',
                type: 'rate_limit_error',
              },
            ].map((preset) => (
              <Button
                key={preset.code}
                type='button'
                variant='outline'
                size='sm'
                className='h-6 text-[10px]'
                onClick={() =>
                  returnErrorEditorProps.updateDraft(returnErrorEditorProps.operationId, {
                    statusCode: preset.statusCode,
                    code: preset.code,
                    type: preset.type,
                  })
                }
              >
                {t(preset.label)}
              </Button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// PruneObjectsEditor
// ---------------------------------------------------------------------------

type PruneObjectsEditorProps = {
  operationId: string;
  draft: PruneObjectsDraft;
  updateDraft: (operationId: string, updater: Partial<PruneObjectsDraft> | ((draft: PruneObjectsDraft) => PruneObjectsDraft)) => void;
  addRule: (operationId: string) => void;
  updateRule: (operationId: string, ruleId: string, patch: Partial<PruneRule>) => void;
  removeRule: (operationId: string, ruleId: string) => void;
};

function PruneObjectsEditor(pruneObjectsEditorProps: PruneObjectsEditorProps) {
  const { t } = useTranslation();
  const draft = pruneObjectsEditorProps.draft;

  return (
    <div className='rounded-lg border p-3'>
      <div className='mb-2 flex items-center justify-between'>
        <span className='text-sm font-medium'>{t('channels.paramOverride.pruneObjects.title')}</span>
        <div className='flex items-center gap-1'>
          <span className='text-muted-foreground text-xs'>{t('channels.paramOverride.labels.mode')}</span>
          <Button
            type='button'
            variant={draft.simpleMode ? 'default' : 'outline'}
            size='sm'
            className='h-7 text-xs'
            onClick={() => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { simpleMode: true })}
          >
            {t('channels.paramOverride.modes.simple')}
          </Button>
          <Button
            type='button'
            variant={draft.simpleMode ? 'outline' : 'default'}
            size='sm'
            className='h-7 text-xs'
            onClick={() => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { simpleMode: false })}
          >
            {t('channels.paramOverride.modes.advanced')}
          </Button>
        </div>
      </div>

      <div className='space-y-1.5'>
        <label className='text-xs font-medium'>{t('channels.paramOverride.pruneObjects.typeCommon')}</label>
        <Input
          value={draft.typeText}
          onChange={(e) => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { typeText: e.target.value })}
          placeholder='redacted_thinking'
          className='h-8 text-xs'
        />
      </div>

      {draft.simpleMode ? (
        <p className='text-muted-foreground mt-2 text-xs'>{t('channels.paramOverride.pruneObjects.simpleHelp')}</p>
      ) : (
        <>
          <div className='mt-3 grid gap-3 sm:grid-cols-2'>
            <div className='space-y-1'>
              <label className='text-xs font-medium'>{t('channels.paramOverride.labels.logic')}</label>
              <Select
                value={draft.logic}
                onValueChange={(v) => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { logic: v || 'AND' })}
              >
                <SelectTrigger className='h-8 text-xs'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='AND'>{t('channels.paramOverride.logic.allMustMatch')}</SelectItem>
                    <SelectItem value='OR'>{t('channels.paramOverride.logic.anyMatch')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='space-y-1'>
              <label className='text-xs font-medium'>{t('channels.paramOverride.pruneObjects.recursionStrategy')}</label>
              <div className='flex gap-1'>
                <Button
                  type='button'
                  variant={draft.recursive ? 'default' : 'outline'}
                  size='sm'
                  className='h-8 text-xs'
                  onClick={() => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { recursive: true })}
                >
                  {t('channels.paramOverride.pruneObjects.recursive')}
                </Button>
                <Button
                  type='button'
                  variant={draft.recursive ? 'outline' : 'default'}
                  size='sm'
                  className='h-8 text-xs'
                  onClick={() => pruneObjectsEditorProps.updateDraft(pruneObjectsEditorProps.operationId, { recursive: false })}
                >
                  {t('channels.paramOverride.pruneObjects.currentLevelOnly')}
                </Button>
              </div>
            </div>
          </div>

          <div className='bg-muted/30 mt-3 rounded-md border p-2'>
            <div className='mb-2 flex items-center justify-between'>
              <span className='text-xs font-medium'>{t('channels.paramOverride.pruneObjects.additionalConditions')}</span>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-7 text-xs'
                onClick={() => pruneObjectsEditorProps.addRule(pruneObjectsEditorProps.operationId)}
              >
                <Plus className='mr-1 h-3 w-3' />
                {t('channels.paramOverride.actions.addCondition')}
              </Button>
            </div>
            {draft.rules.length === 0 ? (
              <p className='text-muted-foreground text-xs'>{t('channels.paramOverride.pruneObjects.noAdditionalConditions')}</p>
            ) : (
              <div className='space-y-2'>
                {draft.rules.map((rule, ruleIndex) => (
                  <div key={rule.id} className='bg-background rounded-md border p-2'>
                    <div className='mb-1 flex items-center justify-between'>
                      <Badge variant='outline' className='text-[10px]'>
                        R{ruleIndex + 1}
                      </Badge>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        className='text-destructive hover:text-destructive h-6 text-[10px]'
                        onClick={() => pruneObjectsEditorProps.removeRule(pruneObjectsEditorProps.operationId, rule.id)}
                      >
                        <Trash2 className='mr-1 h-3 w-3' />
                        {t('channels.paramOverride.actions.delete')}
                      </Button>
                    </div>
                    <div className='grid gap-2 sm:grid-cols-3'>
                      <div className='space-y-0.5'>
                        <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.fieldPath')}</label>
                        <Input
                          value={rule.path}
                          onChange={(e) =>
                            pruneObjectsEditorProps.updateRule(pruneObjectsEditorProps.operationId, rule.id, { path: e.target.value })
                          }
                          placeholder='type'
                          className='h-7 text-xs'
                        />
                      </div>
                      <div className='space-y-0.5'>
                        <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.matchMode')}</label>
                        <Select
                          value={rule.mode}
                          onValueChange={(v) =>
                            v !== null && pruneObjectsEditorProps.updateRule(pruneObjectsEditorProps.operationId, rule.id, { mode: v })
                          }
                        >
                          <SelectTrigger className='h-7 text-xs'>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {CONDITION_MODE_OPTIONS.map((o) => (
                                <SelectItem key={o.value} value={o.value}>
                                  {t(o.label)}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </div>
                      <div className='space-y-0.5'>
                        <label className='text-[10px] font-medium'>{t('channels.paramOverride.labels.matchValueOptional')}</label>
                        <Input
                          value={rule.value_text}
                          onChange={(e) =>
                            pruneObjectsEditorProps.updateRule(pruneObjectsEditorProps.operationId, rule.id, { value_text: e.target.value })
                          }
                          placeholder='redacted_thinking'
                          className='h-7 text-xs'
                        />
                      </div>
                    </div>
                    <div className='mt-1.5 flex flex-wrap gap-3'>
                      <label className='flex items-center gap-1.5 text-[10px]'>
                        <Switch
                          checked={rule.invert}
                          onCheckedChange={(checked) =>
                            pruneObjectsEditorProps.updateRule(pruneObjectsEditorProps.operationId, rule.id, { invert: checked })
                          }
                        />
                        {t('channels.paramOverride.labels.invertMatch')}
                      </label>
                      <label className='flex items-center gap-1.5 text-[10px]'>
                        <Switch
                          checked={rule.pass_missing_key}
                          onCheckedChange={(checked) =>
                            pruneObjectsEditorProps.updateRule(pruneObjectsEditorProps.operationId, rule.id, { pass_missing_key: checked })
                          }
                        />
                        {t('channels.paramOverride.labels.passMissingKey')}
                      </label>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SyncFieldsEditor
// ---------------------------------------------------------------------------

type SyncFieldsEditorProps = {
  operationId: string;
  syncFromTarget: { type: string; key: string };
  syncToTarget: { type: string; key: string };
  updateOperation: (operationId: string, patch: Partial<ParamRule>) => void;
};

function SyncFieldsEditor(syncFieldsEditorProps: SyncFieldsEditorProps) {
  const { t } = useTranslation();
  return (
    <div className='space-y-3'>
      <label className='text-xs font-medium'>{t('channels.paramOverride.syncFields.title')}</label>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-1.5'>
          <label className='text-[10px] font-medium'>{t('channels.paramOverride.syncFields.sourceEndpoint')}</label>
          <div className='flex gap-2'>
            <Select
              value={syncFieldsEditorProps.syncFromTarget.type || 'json'}
              onValueChange={(v) =>
                v !== null &&
                syncFieldsEditorProps.updateOperation(syncFieldsEditorProps.operationId, {
                  from: buildSyncTargetSpec(v, syncFieldsEditorProps.syncFromTarget.key),
                })
              }
            >
              <SelectTrigger className='h-8 w-[110px] text-xs'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {SYNC_TARGET_TYPE_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(o.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Input
              value={syncFieldsEditorProps.syncFromTarget.key}
              onChange={(e) =>
                syncFieldsEditorProps.updateOperation(syncFieldsEditorProps.operationId, {
                  from: buildSyncTargetSpec(syncFieldsEditorProps.syncFromTarget.type, e.target.value),
                })
              }
              placeholder='session_id'
              className='h-8 text-xs'
            />
          </div>
        </div>
        <div className='space-y-1.5'>
          <label className='text-[10px] font-medium'>{t('channels.paramOverride.syncFields.targetEndpoint')}</label>
          <div className='flex gap-2'>
            <Select
              value={syncFieldsEditorProps.syncToTarget.type || 'json'}
              onValueChange={(v) =>
                v !== null &&
                syncFieldsEditorProps.updateOperation(syncFieldsEditorProps.operationId, {
                  to: buildSyncTargetSpec(v, syncFieldsEditorProps.syncToTarget.key),
                })
              }
            >
              <SelectTrigger className='h-8 w-[110px] text-xs'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {SYNC_TARGET_TYPE_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(o.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Input
              value={syncFieldsEditorProps.syncToTarget.key}
              onChange={(e) =>
                syncFieldsEditorProps.updateOperation(syncFieldsEditorProps.operationId, {
                  to: buildSyncTargetSpec(syncFieldsEditorProps.syncToTarget.type, e.target.value),
                })
              }
              placeholder='prompt_cache_key'
              className='h-8 text-xs'
            />
          </div>
        </div>
      </div>
      <div className='flex flex-wrap gap-1'>
        {[
          {
            label: 'header:session_id -> json:prompt_cache_key',
            from: 'header:session_id',
            to: 'json:prompt_cache_key',
          },
          {
            label: 'json:prompt_cache_key -> header:session_id',
            from: 'json:prompt_cache_key',
            to: 'header:session_id',
          },
        ].map((preset) => (
          <Button
            key={preset.label}
            type='button'
            variant='outline'
            size='sm'
            className='h-6 text-[10px]'
            onClick={() => syncFieldsEditorProps.updateOperation(syncFieldsEditorProps.operationId, { from: preset.from, to: preset.to })}
          >
            {preset.label}
          </Button>
        ))}
      </div>
    </div>
  );
}
