import { useEffect, useState, type ReactNode } from 'react';
import { ChevronDown } from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';

interface CollapsibleSectionProps {
  title: string;
  icon: ReactNode;
  children: ReactNode;
  storageKey: string;
  defaultOpen?: boolean;
  // Persisted-open-state prefix. Keeps the dashboard's own sections independent
  // from the ones rendered on /analytics after the global blocks moved there.
  storagePrefix?: string;
}

export function CollapsibleSection({
  title,
  icon,
  children,
  storageKey,
  defaultOpen = false,
  storagePrefix = 'dashboard-section',
}: CollapsibleSectionProps) {
  const [isOpen, setIsOpen] = useState(() => {
    try {
      const stored = localStorage.getItem(`${storagePrefix}-${storageKey}`);
      return stored !== null ? stored === 'true' : defaultOpen;
    } catch {
      return defaultOpen;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(`${storagePrefix}-${storageKey}`, isOpen.toString());
    } catch {
      // Silently fail - persistence is a nice-to-have, not critical
    }
  }, [isOpen, storageKey, storagePrefix]);

  return (
    <div className='space-y-4'>
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className='flex w-full items-center justify-between rounded-lg border bg-card p-4 text-left transition-colors hover:bg-accent/50'
      >
        <div className='flex items-center gap-3'>
          <div className='flex h-8 w-8 items-center justify-center rounded-md bg-primary/10'>
            {icon}
          </div>
          <span className='text-lg font-semibold'>{title}</span>
        </div>
        <motion.div
          animate={{ rotate: isOpen ? 180 : 0 }}
          transition={{ duration: 0.2, ease: 'easeInOut' }}
        >
          <ChevronDown className='h-5 w-5 text-muted-foreground' />
        </motion.div>
      </button>
      <AnimatePresence initial={false}>
        {isOpen && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15, ease: 'easeInOut' }}
          >
            <div className='space-y-4'>{children}</div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
