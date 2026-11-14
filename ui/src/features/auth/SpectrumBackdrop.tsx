// The login backdrop: the 2.4 GHz band as an engineer sketches it, thirteen
// overlapping 22 MHz channel masks from 2412 to 2472 MHz, with the three that
// do not overlap (1, 6 and 11) picked out. It stretches to the viewport
// (strokes stay hairline); decorative only.

const FIRST = 2412
const STEP = 5
const HALF = 11 // half of a 22 MHz DSSS mask
const LEFT = FIRST - HALF - 4
const SPAN = 12 * STEP + 2 * (HALF + 4)
const CLEAR = [1, 6, 11]
const CHANNELS = Array.from({ length: 13 }, (_, i) => i + 1)

const centre = (channel: number) => FIRST + (channel - 1) * STEP - LEFT

function hump(channel: number) {
  const c = centre(channel)
  const top = CLEAR.includes(channel) ? 8 : 26
  return `M${c - HALF} 100 C${c - HALF * 0.5} 100 ${c - HALF * 0.62} ${top} ${c} ${top} C${c + HALF * 0.62} ${top} ${c + HALF * 0.5} 100 ${c + HALF} 100`
}

export function SpectrumBackdrop() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 -z-10 h-[38vh] min-h-40 max-md:h-[22vh]">
      <svg viewBox={`0 0 ${SPAN} 100`} preserveAspectRatio="none" className="absolute inset-x-0 top-0 bottom-7 h-[calc(100%-1.75rem)] w-full">
        <g fill="none" className="stroke-muted-foreground/30">
          {CHANNELS.filter((ch) => !CLEAR.includes(ch)).map((ch) => (
            <path key={ch} d={hump(ch)} strokeWidth="1" vectorEffect="non-scaling-stroke" />
          ))}
        </g>
        <g className="spectrum-clear fill-brand/[0.06] stroke-brand">
          {CLEAR.map((ch) => (
            <path key={ch} d={hump(ch)} strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
          ))}
        </g>
        <line x1="0" x2={SPAN} y1="100" y2="100" strokeWidth="1" vectorEffect="non-scaling-stroke" className="stroke-border" />
      </svg>
      <div className="absolute inset-x-0 bottom-0 h-7 text-xs text-muted-foreground tabular-nums">
        {CLEAR.map((ch) => (
          <span key={ch} className="absolute top-1.5 -translate-x-1/2" style={{ left: `${(centre(ch) / SPAN) * 100}%` }}>
            {ch}
          </span>
        ))}
        <span className="absolute top-1.5 right-4 max-sm:hidden">2.4 GHz</span>
      </div>
    </div>
  )
}
