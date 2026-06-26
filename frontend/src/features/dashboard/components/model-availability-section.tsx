import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ColumnDef,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type SortingState,
} from '@tanstack/react-table';
import { Activity, CheckCircle2, XCircle, HelpCircle } from 'lucide-react';
import * as Icons from '@lobehub/icons';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { DataTableColumnHeader } from '@/components/data-table-column-header';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import {
  useModelAvailability,
  type ModelAvailabilityItem,
} from '../data/model-availability';
import { resolveModelIcon } from '../data/model-icon';
import { ModelHealthBars } from './model-health-bars';

// ModelAvailabilitySection is a sanitized, channel-free table of per-model
// availability. Sortable headers (asc/desc/hide) like a conventional table.
// No channel/provider/pricing information.
export function ModelAvailabilitySection() {
  const { t } = useTranslation();
  const { data, isLoading } = useModelAvailability();
  const [sorting, setSorting] = useState<SortingState>([]);

  const columns: ColumnDef<ModelAvailabilityItem>[] = [
    {
      accessorKey: 'displayName',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colModel')} />
      ),
      cell: ({ row }) => {
        const m = row.original;
        const iconName = resolveModelIcon(m.icon, m.modelId);
        const IconComponent = iconName && Icons[iconName as keyof typeof Icons];
        return (
          <div className='flex items-center justify-center gap-2'>
            {IconComponent ? (
              <IconComponent className='h-5 w-5 shrink-0' />
            ) : (
              <span className='text-muted-foreground text-xs'>-</span>
            )}
            <div className='flex flex-col items-center gap-1'>
              <span>{m.displayName}</span>
              {(m.capabilities?.vision ||
                m.capabilities?.toolCall ||
                m.capabilities?.reasoning) && (
                <div className='flex flex-wrap justify-center gap-1'>
                  {m.capabilities?.vision && (
                    <Badge variant='outline' className='text-[10px]'>
                      {t('projectUsage.modelAvailability.vision')}
                    </Badge>
                  )}
                  {m.capabilities?.toolCall && (
                    <Badge variant='outline' className='text-[10px]'>
                      {t('projectUsage.modelAvailability.toolCall')}
                    </Badge>
                  )}
                  {m.capabilities?.reasoning && (
                    <Badge variant='outline' className='text-[10px]'>
                      {t('projectUsage.modelAvailability.reasoning')}
                    </Badge>
                  )}
                </div>
              )}
            </div>
          </div>
        );
      },
    },
    {
      accessorKey: 'latestStatus',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colLatestStatus')} />
      ),
      cell: ({ row }) => {
        const m = row.original;
        const map = {
          success: { icon: CheckCircle2, class: 'text-emerald-500', label: t('projectUsage.modelAvailability.statusSuccess') },
          error: { icon: XCircle, class: 'text-red-500', label: t('projectUsage.modelAvailability.statusError') },
          unknown: { icon: HelpCircle, class: 'text-muted-foreground', label: t('projectUsage.modelAvailability.statusUnknown') },
        } as const;
        const s = map[(m.latestStatus as keyof typeof map) ?? 'unknown'] ?? map.unknown;
        const Icon = s.icon;
        return (
          <div className={cn('flex items-center justify-center gap-1 text-sm font-medium', s.class)}>
            <Icon className='h-4 w-4' />
            {s.label}
          </div>
        );
      },
    },
    {
      accessorKey: 'latestLatencyMs',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colLatestLatency')} />
      ),
      cell: ({ row }) => (
        <div className='text-center text-sm text-muted-foreground'>
          {row.original.latestLatencyMs != null ? formatDuration(row.original.latestLatencyMs) : '-'}
        </div>
      ),
    },
    {
      accessorKey: 'successRate',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colSuccessRate')} />
      ),
      cell: ({ row }) => {
        const rate = row.original.successRate != null ? row.original.successRate * 100 : null;
        return (
          <div className='text-center text-sm font-medium'>
            {rate != null ? `${rate.toFixed(1)}%` : '-'}
          </div>
        );
      },
    },
    {
      accessorKey: 'avgLatencyMs',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colAvgLatency')} />
      ),
      cell: ({ row }) => (
        <div className='text-center text-sm text-muted-foreground'>
          {row.original.avgLatencyMs != null ? formatDuration(row.original.avgLatencyMs) : '-'}
        </div>
      ),
    },
    {
      id: 'healthPoints',
      header: ({ column }) => (
        <DataTableColumnHeader className='w-full justify-center' column={column} title={t('projectUsage.modelAvailability.colHealth')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <div className='flex justify-center'>
          <ModelHealthBars points={row.original.healthPoints ?? []} />
        </div>
      ),
    },
  ];

  const table = useReactTable({
    data: data ?? [],
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  return (
    <Card className='hover-card'>
      <CardHeader>
        <div className='flex items-center gap-2'>
          <div className='flex h-8 w-8 items-center justify-center rounded-md bg-primary/10'>
            <Activity className='h-4 w-4 text-primary' />
          </div>
          <div className='space-y-1'>
            <CardTitle>{t('projectUsage.modelAvailability.title')}</CardTitle>
            <CardDescription>{t('projectUsage.modelAvailability.description')}</CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className='space-y-2'>
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className='h-12' />
            ))}
          </div>
        ) : (data?.length ?? 0) === 0 ? (
          <p className='text-muted-foreground py-10 text-center text-sm'>
            {t('projectUsage.modelAvailability.empty')}
          </p>
        ) : (
          <div className='overflow-x-auto'>
            <Table>
              <TableHeader>
                {table.getHeaderGroups().map((headerGroup) => (
                  <TableRow key={headerGroup.id}>
                    {headerGroup.headers.map((header) => (
                      <TableHead key={header.id} className='text-center'>
                        {header.isPlaceholder
                          ? null
                          : flexRender(header.column.columnDef.header, header.getContext())}
                      </TableHead>
                    ))}
                  </TableRow>
                ))}
              </TableHeader>
              <TableBody>
                {table.getRowModel().rows.map((row) => (
                  <TableRow key={row.id}>
                    {row.getVisibleCells().map((cell) => (
                      <TableCell key={cell.id}>
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
