import type { ComponentType, ReactNode, SVGProps } from "react";

export type IconProps = SVGProps<SVGSVGElement> & { size?: number };
export type AppIcon = ComponentType<IconProps>;

type IconFrameProps = IconProps & { children: ReactNode };

function IconFrame({ size = 18, children, ...props }: IconFrameProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      {children}
    </svg>
  );
}

export function GatewayMark(props: IconProps) {
  return <IconFrame {...props}><path d="M6.5 5.5h11A1.5 1.5 0 0 1 19 7v10a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 5 17V7a1.5 1.5 0 0 1 1.5-1.5Z" /><path d="M8.5 9.5h7M8.5 12h7M8.5 14.5h4" /><path d="M3 8.5v7M21 8.5v7" /></IconFrame>;
}

export function OverviewIcon(props: IconProps) {
  return <IconFrame {...props}><path d="M4 15.5c2.1 0 2.1-7 4.2-7s2.1 5 4.2 5 2.1-8 4.2-8 2.1 4 3.4 4" /><path d="M4 19.5h16" /></IconFrame>;
}

export function ProvidersIcon(props: IconProps) {
  return <IconFrame {...props}><rect x="4" y="4" width="16" height="6" rx="1.5" /><rect x="4" y="14" width="16" height="6" rx="1.5" /><path d="M7 7h.01M7 17h.01M10 7h6M10 17h6" /></IconFrame>;
}

export function RoutesIcon(props: IconProps) {
  return <IconFrame {...props}><circle cx="5" cy="12" r="2" /><circle cx="19" cy="6" r="2" /><circle cx="19" cy="18" r="2" /><path d="M7 12h4c2.2 0 3-6 6-6M11 12c2.2 0 3 6 6 6" /></IconFrame>;
}

export function ClientsIcon(props: IconProps) {
  return <IconFrame {...props}><rect x="4" y="5" width="11" height="14" rx="1.5" /><path d="m8 10 2 2-2 2M11 14h2" /><path d="M17 8.5h2.5M17 12h2.5M17 15.5h2.5" /></IconFrame>;
}

export function LogsIcon(props: IconProps) {
  return <IconFrame {...props}><path d="M7 3.5h7l3.5 3.5v13.5H7A1.5 1.5 0 0 1 5.5 19V5A1.5 1.5 0 0 1 7 3.5Z" /><path d="M14 3.5V7h3.5M8.5 11h6M8.5 14h6M8.5 17h3.5" /></IconFrame>;
}

export function UsageIcon(props: IconProps) {
  return <IconFrame {...props}><path d="M5 19.5V13M9.5 19.5V9M14 19.5V5M18.5 19.5v-8" /><path d="M4 19.5h16" /></IconFrame>;
}

export function SettingsIcon(props: IconProps) {
  return <IconFrame {...props}><path d="M4 7h16M4 12h16M4 17h16" /><circle cx="9" cy="7" r="1.75" fill="currentColor" stroke="none" /><circle cx="15" cy="12" r="1.75" fill="currentColor" stroke="none" /><circle cx="11" cy="17" r="1.75" fill="currentColor" stroke="none" /></IconFrame>;
}
