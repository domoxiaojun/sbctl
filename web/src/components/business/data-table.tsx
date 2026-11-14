import { useMemo, type ReactNode } from 'react'
import { createColumnHelper, tableFeatures, useTable, type RowData } from '@tanstack/react-table'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DataState } from './common'
const features = tableFeatures({})
export type DisplayColumn<T> = { id: string; label: string; render: (row: T) => ReactNode }
export function DataTable<T extends RowData>({ data, columns, rowId }: { data: T[]; columns: DisplayColumn<T>[]; rowId: (row: T) => string }) {
  const defs = useMemo(() => { const helper = createColumnHelper<typeof features, T>(); return helper.columns(columns.map(column => helper.display({ id: column.id, header: column.label, cell: info => column.render(info.row.original) }))) }, [columns])
  const table = useTable({ features, data, columns: defs, getRowId: rowId })
  if (!data.length) return <DataState />
  return <Table><TableHeader>{table.getHeaderGroups().map(group => <TableRow key={group.id}>{group.headers.map(header => <TableHead key={header.id}><table.FlexRender header={header} /></TableHead>)}</TableRow>)}</TableHeader><TableBody>{table.getRowModel().rows.map(row => <TableRow key={row.id}>{row.getAllCells().map(cell => <TableCell key={cell.id}><table.FlexRender cell={cell} /></TableCell>)}</TableRow>)}</TableBody></Table>
}
