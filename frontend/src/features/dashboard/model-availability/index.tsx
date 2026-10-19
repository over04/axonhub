import { useTranslation } from 'react-i18next';
import { Header } from '@/components/layout/header';
import { ModelAvailabilitySection } from '../components/model-availability-section';

// ModelAvailabilityPage is the standalone, system-level model availability view.
// The section used to be embedded in the dashboard; it now lives here so the
// dashboard only keeps personal / current-project content.
export default function ModelAvailabilityPage() {
  const { t } = useTranslation();

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header />
      <div className='space-y-1'>
        <h1 className='text-2xl font-bold tracking-tight'>{t('modelAvailability.title')}</h1>
        <p className='text-muted-foreground text-sm'>{t('modelAvailability.description')}</p>
      </div>
      <ModelAvailabilitySection />
    </div>
  );
}
