const LOCKUP_SRC = "/brand/doops-lockup-dark.png"
const STACKED_SRC = "/brand/doops-stacked-dark.png"
const MARK_SRC = "/brand/doops-mark.png"

// Intrinsic sizes of the generated assets; keep them in sync when re-exporting
// the logo so the browser reserves the right space before the PNG decodes.
const LOCKUP_RATIO = 1323 / 470
const STACKED_RATIO = 724 / 718
const MARK_RATIO = 1

type BrandLogoProps = {
  height?: number
  className?: string
}

/** Horizontal DoOps lockup (mark + wordmark), tuned for dark surfaces. */
export function BrandLockup({ height = 28, className = "" }: BrandLogoProps) {
  return (
    <img
      src={LOCKUP_SRC}
      alt="DoOps"
      width={Math.round(height * LOCKUP_RATIO)}
      height={height}
      style={{ height }}
      draggable={false}
      className={`w-auto shrink-0 select-none ${className}`}
    />
  )
}

/** Stacked DoOps lockup for hero areas (mark above the wordmark). */
export function BrandStacked({ height = 96, className = "" }: BrandLogoProps) {
  return (
    <img
      src={STACKED_SRC}
      alt="DoOps"
      width={Math.round(height * STACKED_RATIO)}
      height={height}
      style={{ height }}
      draggable={false}
      className={`w-auto shrink-0 select-none ${className}`}
    />
  )
}

/** Mark-only brand asset for compact rows and favicon-sized slots. */
export function BrandMark({ height = 28, className = "" }: BrandLogoProps) {
  return (
    <img
      src={MARK_SRC}
      alt=""
      aria-hidden="true"
      width={Math.round(height * MARK_RATIO)}
      height={height}
      style={{ height }}
      draggable={false}
      className={`shrink-0 select-none ${className}`}
    />
  )
}
