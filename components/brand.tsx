import { useId } from "react"

type BrandLogoProps = {
  height?: number
  className?: string
}

// Crisp geometric paths for the DoOps interlocking "D >" emblem in a 0..100 box.
// Blue upper-right ribbon extends slightly beneath the teal overlap zones so
// subpixel rasterization never shows a seam at any DPI or scale.
const BLUE_RIBBON_PATH =
  "M 4 17 A 11 11 0 0 1 15 6 L 51 6 C 79 6, 96 23, 96 50 C 96 56, 94 61, 91 66 C 82 72, 68 75, 48 75 L 49 73 L 67.8 54.2 Q 72 50, 67.8 45.8 L 51.8 29.8 Q 49 27, 45 27 L 14 27 L 10 38 L 7.5 36.5 C 5.2 33.8, 4 30, 4 25 Z"

const TEAL_RIBBON_PATH =
  "M 15 27 L 28.5 27 L 47.3 45.8 Q 51.5 50, 47.3 54.2 L 28.5 73 L 52 73 C 74 73, 91 64, 95.8 47.5 C 96.1 49, 96 50, 96 50 C 96 77, 79 94, 51 94 L 15 94 A 11 11 0 0 1 4 83 L 4 72.5 Q 4 67, 8 63 L 21 50 L 9.5 38.5 C 6.5 35.5, 8.5 27, 15 27 Z"

const MARK_HIGHLIGHT_PATH =
  "M 15 7.5 L 51 7.5 C 76.5 7.5, 93.2 22.8, 94.4 47"

const WORDMARK_FONT =
  "var(--font-sans), 'Geist', 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"

function DoOpsMarkPaths({ idPrefix }: { idPrefix: string }) {
  const blueId = `${idPrefix}-blue`
  const tealId = `${idPrefix}-teal`
  const shineId = `${idPrefix}-shine`
  return (
    <>
      <defs>
        <linearGradient id={blueId} x1="6" y1="6" x2="94" y2="74" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#3B82FF" />
          <stop offset="52%" stopColor="#1E5CFF" />
          <stop offset="100%" stopColor="#1442DC" />
        </linearGradient>
        <linearGradient id={tealId} x1="8" y1="27" x2="94" y2="94" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#2CF0E4" />
          <stop offset="50%" stopColor="#06D0D2" />
          <stop offset="100%" stopColor="#00AEC4" />
        </linearGradient>
        <linearGradient id={shineId} x1="15" y1="7" x2="94" y2="47" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.34" />
          <stop offset="100%" stopColor="#FFFFFF" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={BLUE_RIBBON_PATH} fill={`url(#${blueId})`} />
      <path
        d={MARK_HIGHLIGHT_PATH}
        fill="none"
        stroke={`url(#${shineId})`}
        strokeWidth="1.5"
        strokeLinecap="round"
      />
      <path d={TEAL_RIBBON_PATH} fill={`url(#${tealId})`} />
    </>
  )
}

/** Horizontal DoOps vector lockup (crisp SVG mark + wordmark), tuned for dark headers. */
export function BrandLockup({ height = 28, className = "" }: BrandLogoProps) {
  const uid = useId().replace(/:/g, "")
  const viewWidth = 158
  const viewHeight = 44
  const width = Math.round((height * viewWidth) / viewHeight)

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox={`0 0 ${viewWidth} ${viewHeight}`}
      width={width}
      height={height}
      role="img"
      aria-label="DoOps"
      shapeRendering="geometricPrecision"
      textRendering="geometricPrecision"
      style={{ height }}
      className={`w-auto shrink-0 select-none ${className}`}
    >
      <g transform="translate(2, 3) scale(0.38)">
        <DoOpsMarkPaths idPrefix={`${uid}-lockup`} />
      </g>
      <text
        x="49"
        y="31.5"
        fill="#F8FAFC"
        fontFamily={WORDMARK_FONT}
        fontSize="27.5"
        fontWeight="700"
        letterSpacing="-0.75"
      >
        Do
        <tspan fill="#DDEEFF">Ops</tspan>
      </text>
    </svg>
  )
}

/** Stacked DoOps vector lockup for login & hero surfaces (mark above the wordmark). */
export function BrandStacked({ height = 96, className = "" }: BrandLogoProps) {
  const uid = useId().replace(/:/g, "")
  const viewWidth = 148
  const viewHeight = 144
  const width = Math.round((height * viewWidth) / viewHeight)

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox={`0 0 ${viewWidth} ${viewHeight}`}
      width={width}
      height={height}
      role="img"
      aria-label="DoOps"
      shapeRendering="geometricPrecision"
      textRendering="geometricPrecision"
      style={{ height }}
      className={`w-auto shrink-0 select-none ${className}`}
    >
      <g transform="translate(31, 4) scale(0.86)">
        <DoOpsMarkPaths idPrefix={`${uid}-stacked`} />
      </g>
      <text
        x="74"
        y="131"
        textAnchor="middle"
        fill="#F8FAFC"
        fontFamily={WORDMARK_FONT}
        fontSize="36"
        fontWeight="700"
        letterSpacing="-0.9"
      >
        Do
        <tspan fill="#DDEEFF">Ops</tspan>
      </text>
    </svg>
  )
}

/** Mark-only crisp vector emblem for compact rows and icon slots. */
export function BrandMark({ height = 28, className = "" }: BrandLogoProps) {
  const uid = useId().replace(/:/g, "")
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 100 100"
      width={height}
      height={height}
      aria-hidden="true"
      shapeRendering="geometricPrecision"
      style={{ height }}
      className={`shrink-0 select-none ${className}`}
    >
      <DoOpsMarkPaths idPrefix={`${uid}-mark`} />
    </svg>
  )
}
