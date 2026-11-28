// Pure client-side pagination helpers for the machine asset ledger.

export const MACHINE_PAGE_SIZE_OPTIONS = [20, 50, 100, 200] as const
export type MachinePageSize = (typeof MACHINE_PAGE_SIZE_OPTIONS)[number]
export const DEFAULT_MACHINE_PAGE_SIZE: MachinePageSize = 50

export interface PaginatedSlice<T> {
  items: T[]
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
  /** 1-based inclusive start index, or 0 when empty. */
  startIndex: number
  /** 1-based inclusive end index, or 0 when empty. */
  endIndex: number
  hasPrev: boolean
  hasNext: boolean
}

export function normalizePageSize(pageSize: number): number {
  if (!Number.isFinite(pageSize) || pageSize < 1) return DEFAULT_MACHINE_PAGE_SIZE
  return Math.floor(pageSize)
}

export function paginateItems<T>(
  items: readonly T[],
  requestedPage: number,
  requestedPageSize: number = DEFAULT_MACHINE_PAGE_SIZE,
): PaginatedSlice<T> {
  const pageSize = normalizePageSize(requestedPageSize)
  const totalItems = items.length
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize))
  const safeRequested = Number.isFinite(requestedPage) ? Math.floor(requestedPage) : 1
  const page = Math.min(Math.max(1, safeRequested), totalPages)
  if (totalItems === 0) {
    return {
      items: [],
      page: 1,
      pageSize,
      totalItems: 0,
      totalPages: 1,
      startIndex: 0,
      endIndex: 0,
      hasPrev: false,
      hasNext: false,
    }
  }
  const offset = (page - 1) * pageSize
  const slice = items.slice(offset, offset + pageSize)
  return {
    items: slice,
    page,
    pageSize,
    totalItems,
    totalPages,
    startIndex: offset + 1,
    endIndex: offset + slice.length,
    hasPrev: page > 1,
    hasNext: page < totalPages,
  }
}

export function visiblePageNumbers(current: number, total: number): Array<number | "ellipsis"> {
  if (total <= 1) return [1]
  const clamped = Math.min(Math.max(1, Math.floor(current)), total)
  if (total <= 7) {
    return Array.from({ length: total }, (_, idx) => idx + 1)
  }
  const pages: Array<number | "ellipsis"> = [1]
  const left = Math.max(2, clamped - 1)
  const right = Math.min(total - 1, clamped + 1)
  if (left > 2) pages.push("ellipsis")
  for (let p = left; p <= right; p += 1) pages.push(p)
  if (right < total - 1) pages.push("ellipsis")
  pages.push(total)
  return pages
}
