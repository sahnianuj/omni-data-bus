/** The Omni-Data-Bus mark: one black backbone with endpoints tapping into it, on school-bus yellow (matches public/favicon.svg). */
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden focusable="false">
      <rect width="32" height="32" rx="8" fill="#FFB400" />
      <rect x="3" y="14" width="26" height="4" rx="2" fill="#111111" />
      <path d="M10 11v4M22 11v4M16 17v4" stroke="#111111" strokeWidth="2.5" />
      <circle cx="10" cy="8" r="3.5" fill="#111111" />
      <circle cx="22" cy="8" r="3.5" fill="#111111" />
      <circle cx="16" cy="24" r="3.5" fill="#111111" />
      <circle cx="10" cy="8" r="1.4" fill="#FFB400" />
      <circle cx="22" cy="8" r="1.4" fill="#FFB400" />
      <circle cx="16" cy="24" r="1.4" fill="#FFB400" />
    </svg>
  )
}
