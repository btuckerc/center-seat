import type { SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 20, children, ...props }: IconProps & { children: React.ReactNode }) {
  return (
    <svg aria-hidden="true" fill="none" focusable="false" height={size} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8} viewBox="0 0 24 24" width={size} {...props}>
      {children}
    </svg>
  );
}

export const SearchIcon = (props: IconProps) => <Svg {...props}><circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.2-4.2" /></Svg>;
export const CloseIcon = (props: IconProps) => <Svg {...props}><path d="M6 6l12 12M18 6 6 18" /></Svg>;
export const PinIcon = (props: IconProps) => <Svg {...props}><path d="M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.4-6.5 11-6.5 11Z" /><circle cx="12" cy="10" r="2.3" /></Svg>;
export const LocateIcon = (props: IconProps) => <Svg {...props}><circle cx="12" cy="12" r="6" /><circle cx="12" cy="12" r="1.6" fill="currentColor" /><path d="M12 2.5V5M12 19v2.5M2.5 12H5M19 12h2.5" /></Svg>;
export const CalendarIcon = (props: IconProps) => <Svg {...props}><rect height="15" rx="2.5" width="16" x="4" y="5.5" /><path d="M4 10h16M8.5 3.5v4M15.5 3.5v4" /></Svg>;
export const MinusIcon = (props: IconProps) => <Svg {...props}><path d="M6 12h12" /></Svg>;
export const PlusIcon = (props: IconProps) => <Svg {...props}><path d="M12 6v12M6 12h12" /></Svg>;
export const SlidersIcon = (props: IconProps) => <Svg {...props}><path d="M4 7h9M17 7h3M4 17h3M11 17h9" /><circle cx="15" cy="7" r="2" /><circle cx="9" cy="17" r="2" /></Svg>;
export const ArrowRightIcon = (props: IconProps) => <Svg {...props}><path d="M5 12h14M13 6l6 6-6 6" /></Svg>;
export const ExternalIcon = (props: IconProps) => <Svg {...props}><path d="M14 4h6v6M20 4l-9 9" /><path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" /></Svg>;
export const LinkIcon = (props: IconProps) => <Svg {...props}><path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1" /><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1" /></Svg>;
export const TicketIcon = (props: IconProps) => <Svg {...props}><path d="M4 7.5A1.5 1.5 0 0 1 5.5 6h13A1.5 1.5 0 0 1 20 7.5V10a2 2 0 0 0 0 4v2.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 16.5V14a2 2 0 0 0 0-4Z" /><path d="M14 6v12" strokeDasharray="2 2" /></Svg>;
export const CodeIcon = (props: IconProps) => <Svg {...props}><path d="M8.5 7 4 12l4.5 5M15.5 7 20 12l-4.5 5" /></Svg>;
export const ShareIcon = (props: IconProps) => <Svg {...props}><path d="M12 15V4M8 8l4-4 4 4" /><path d="M5 12v6.5A1.5 1.5 0 0 0 6.5 20h11a1.5 1.5 0 0 0 1.5-1.5V12" /></Svg>;
export const InfoIcon = (props: IconProps) => <Svg {...props}><circle cx="12" cy="12" r="8.5" /><path d="M12 11v5.5" /><circle cx="12" cy="7.8" r=".9" fill="currentColor" stroke="none" /></Svg>;
export const RefreshIcon = (props: IconProps) => <Svg {...props}><path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" /><path d="M19.5 4.5v4h-4" /></Svg>;
export const ChevronLeftIcon = (props: IconProps) => <Svg {...props}><path d="m14.5 6-6 6 6 6" /></Svg>;
export const ChevronRightIcon = (props: IconProps) => <Svg {...props}><path d="m9.5 6 6 6-6 6" /></Svg>;
export const CheckIcon = (props: IconProps) => <Svg {...props}><path d="m5 12.5 4.5 4.5L19 7.5" /></Svg>;
export const LockIcon = (props: IconProps) => <Svg {...props}><rect height="9" rx="2" width="13" x="5.5" y="10.5" /><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5" /></Svg>;
export const ClockIcon = (props: IconProps) => <Svg {...props}><circle cx="12" cy="12" r="8.5" /><path d="M12 7.5V12l3 2" /></Svg>;
export const FilmIcon = (props: IconProps) => <Svg {...props}><rect height="16" rx="2" width="18" x="3" y="4" /><path d="M7 4v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4" /></Svg>;
export const PencilIcon = (props: IconProps) => <Svg {...props}><path d="M4 20h4L19 9l-4-4L4 16Z" /><path d="m13.5 6.5 4 4" /></Svg>;
export const AlertIcon = (props: IconProps) => <Svg {...props}><path d="M12 4 2.8 19.5h18.4Z" /><path d="M12 10v4.5" /><circle cx="12" cy="17" r=".9" fill="currentColor" stroke="none" /></Svg>;
export const SeatIcon = (props: IconProps) => <Svg {...props}><path d="M6.5 12V7.5A2.5 2.5 0 0 1 9 5h6a2.5 2.5 0 0 1 2.5 2.5V12" /><path d="M4.5 11.5h15v4a1.5 1.5 0 0 1-1.5 1.5H6a1.5 1.5 0 0 1-1.5-1.5Z" /><path d="M7 17v2.5M17 17v2.5" /></Svg>;
export const WheelchairIcon = (props: IconProps) => <Svg {...props}><circle cx="11" cy="4.5" r="1.6" /><path d="M11 7.5v6h5l2 5" /><path d="M11 10.5h4.5" /><path d="M8 11.2a5 5 0 1 0 7 6.3" /></Svg>;

/** Critics mark: a simple tomato. */
export const TomatoIcon = (props: IconProps) => (
  <Svg {...props} strokeWidth={0}>
    <circle cx="12" cy="13.5" fill="#e5533d" r="7.5" />
    <path d="M12 6.2c-1.3-1.9-3.4-2.3-4.6-1.6 1.6.3 2.6 1.1 3 2.1-1.8-.3-3.3.4-3.9 1.6 1.8-.6 3.5-.3 5.5.4 2-.7 3.7-1 5.5-.4-.6-1.2-2.1-1.9-3.9-1.6.4-1 1.4-1.8 3-2.1-1.2-.7-3.3-.3-4.6 1.6Z" fill="#5fa548" />
  </Svg>
);

/** Audience mark: a popcorn bucket. */
export const PopcornIcon = (props: IconProps) => (
  <Svg {...props} strokeWidth={0}>
    <circle cx="8" cy="7.5" fill="#f7e2a8" r="2.6" />
    <circle cx="12" cy="6" fill="#f7e2a8" r="2.8" />
    <circle cx="16" cy="7.5" fill="#f7e2a8" r="2.6" />
    <path d="M5.5 9h13l-1.6 11.5H7.1Z" fill="#e5533d" />
    <path d="M9.4 9l.5 11.5M14.6 9l-.5 11.5" stroke="#fff3e2" strokeWidth={1.4} />
  </Svg>
);

/**
 * Brand mark: a screen over three curved rows of seats, the centre seat lit gold.
 * 32-unit tile; 4.5-unit seats on a 6-unit pitch, outer columns raised 0.75 so rows bow toward the screen,
 * and the block (screen apex to last row) is centred vertically.
 */
export const BrandMark = () => (
  <svg aria-hidden="true" className="brand-mark" focusable="false" height="32" viewBox="0 0 32 32" width="32">
    <path className="brand-screen" d="M9.5 7Q16 4.5 22.5 7" />
    {[0, 1, 2].map((row) => [0, 1, 2].map((column) => (
      <rect
        className={row === 1 && column === 1 ? "brand-seat is-lit" : "brand-seat"}
        height="4.5"
        key={`${row}-${column}`}
        rx="1.5"
        width="4.5"
        x={7.75 + column * 6}
        y={10.5 + row * 6 - (column === 1 ? 0 : 0.75)}
      />
    )))}
  </svg>
);
