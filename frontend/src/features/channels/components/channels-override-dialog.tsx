import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useUpdateChannel } from '../data/channels';
import { Channel } from '../data/schema';
import { mergeChannelSettingsForUpdate } from '../utils/merge';
import { ParamOverrideEditorDialog } from './param-override-editor-dialog';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel;
}

function normalizeParamOverride(value: string): string {
  const trimmed = value.trim();
  return trimmed ? JSON.stringify(JSON.parse(trimmed), null, 2) : '';
}

export function ChannelsOverrideDialog({ open, onOpenChange, currentRow }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const [paramOverride, setParamOverride] = useState('');

  useEffect(() => {
    if (open) {
      setParamOverride(currentRow.settings?.paramOverride || '');
    }
  }, [open, currentRow]);

  const handleSave = async (value: string) => {
    const nextValue = normalizeParamOverride(value);
    const nextSettings = mergeChannelSettingsForUpdate(currentRow.settings, {
      paramOverride: nextValue,
    });

    await updateChannel.mutateAsync({
      id: currentRow.id,
      input: { settings: nextSettings },
    });

    setParamOverride(nextValue);
    toast.success(t('channels.messages.updateSuccess'));
    onOpenChange(false);
  };

  return (
    <ParamOverrideEditorDialog
      open={open}
      value={paramOverride}
      onOpenChange={onOpenChange}
      onSave={handleSave}
      saving={updateChannel.isPending}
    />
  );
}
