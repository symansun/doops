"use client"

import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type Column,
  type ColumnDef,
  type ColumnFiltersState,
  type PaginationState,
  type SortingState,
  type Table as TanStackTable,
} from "@tanstack/react-table"
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react"
import { useState, type ReactNode } from "react"
import { cn } from "@/lib/cn"
import { Button } from "./button"
import { Select } from "./select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./table"

export type { ColumnDef }

export function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  className,
}: {
  column: Column<TData, TValue>
  title: string
  className?: string
}) {
  if (!column.getCanSort()) {
    return <span className={className}>{title}</span>
  }
  const sorted = column.getIsSorted()
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className={cn("-ml-2 h-7 px-2 text-xs font-medium text-muted-foreground hover:text-foreground", className)}
      onClick={() => column.toggleSorting(sorted === "asc")}
    >
      <span>{title}</span>
      {sorted === "desc" ? (
        <ArrowDown className="size-3" aria-hidden="true" />
      ) : sorted === "asc" ? (
        <ArrowUp className="size-3" aria-hidden="true" />
      ) : (
        <ArrowUpDown className="size-3 opacity-60" aria-hidden="true" />
      )}
    </Button>
  )
}

export function DataTablePagination<TData>({
  table,
  pageSizeOptions = [20, 50, 100, 200],
  className,
}: {
  table: TanStackTable<TData>
  pageSizeOptions?: readonly number[]
  className?: string
}) {
  const totalRows = table.getFilteredRowModel().rows.length
  const pageIndex = table.getState().pagination.pageIndex
  const pageSize = table.getState().pagination.pageSize
  const pageCount = Math.max(1, table.getPageCount())
  const start = totalRows === 0 ? 0 : pageIndex * pageSize + 1
  const end = Math.min(totalRows, (pageIndex + 1) * pageSize)

  return (
    <div
      data-slot="data-table-pagination"
      className={cn("flex flex-wrap items-center justify-between gap-3 pt-3 text-xs text-muted-foreground", className)}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span>
          显示 <strong className="font-mono text-foreground">{start}–{end}</strong> / 共{" "}
          <strong className="font-mono text-foreground">{totalRows}</strong> 条
        </span>
        <Select
          aria-label="每页显示条数"
          scale="sm"
          className="w-28"
          value={String(pageSize)}
          onChange={(e) => table.setPageSize(Number(e.target.value))}
        >
          {pageSizeOptions.map((size) => (
            <option key={size} value={String(size)}>
              {size} 条/页
            </option>
          ))}
        </Select>
      </div>
      <div className="flex items-center gap-1.5">
        <span className="font-mono text-xs">
          第 {pageIndex + 1} / {pageCount} 页
        </span>
        <Button
          type="button"
          size="sm"
          icon
          aria-label="上一页"
          disabled={!table.getCanPreviousPage()}
          onClick={() => table.previousPage()}
        >
          <ChevronLeft className="size-3.5" aria-hidden="true" />
        </Button>
        <Button
          type="button"
          size="sm"
          icon
          aria-label="下一页"
          disabled={!table.getCanNextPage()}
          onClick={() => table.nextPage()}
        >
          <ChevronRight className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
    </div>
  )
}

export interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[]
  data: TData[]
  empty?: ReactNode
  paginate?: boolean
  initialPageSize?: number
  pageSizeOptions?: readonly number[]
  globalFilter?: string
  toolbar?: (table: TanStackTable<TData>) => ReactNode
  className?: string
  tableClassName?: string
  getRowId?: (originalRow: TData, index: number) => string
}

export function DataTable<TData, TValue>({
  columns,
  data,
  empty = "暂无数据",
  paginate = false,
  initialPageSize = 50,
  pageSizeOptions = [20, 50, 100, 200],
  globalFilter,
  toolbar,
  className,
  tableClassName,
  getRowId,
}: DataTableProps<TData, TValue>) {
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: initialPageSize,
  })

  const table = useReactTable({
    data,
    columns,
    getRowId,
    state: {
      sorting,
      columnFilters,
      ...(globalFilter !== undefined ? { globalFilter } : {}),
      ...(paginate ? { pagination } : {}),
    },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    ...(paginate ? { onPaginationChange: setPagination } : {}),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    ...(paginate ? { getPaginationRowModel: getPaginationRowModel() } : {}),
  })

  const rows = table.getRowModel().rows

  return (
    <div data-slot="data-table" className={cn("min-w-0 space-y-2", className)}>
      {toolbar ? toolbar(table) : null}
      <div className="rounded-lg border border-border bg-card">
        <Table className={tableClassName}>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id} className="hover:bg-transparent">
                {headerGroup.headers.map((header) => (
                  <TableHead key={header.id}>
                    {header.isPlaceholder
                      ? null
                      : flexRender(header.column.columnDef.header, header.getContext())}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {rows.length > 0 ? (
              rows.map((row) => (
                <TableRow key={row.id} data-state={row.getIsSelected() ? "selected" : undefined}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={columns.length} className="h-24 text-center text-sm text-muted-foreground">
                  {empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {paginate && <DataTablePagination table={table} pageSizeOptions={pageSizeOptions} />}
    </div>
  )
}
