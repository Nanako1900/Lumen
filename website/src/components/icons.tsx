import type { SVGProps } from "react";

/**
 * 线性图标集（stroke = currentColor，颜色由 className 的 text-* 控制）。
 * 路径取自设计稿 1a–1f 的内联 SVG（Feather/Lucide 风格），零外部依赖。
 */

export interface IconProps extends SVGProps<SVGSVGElement> {
  size?: number;
}

function Stroke({ size = 24, children, ...props }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...props}
    >
      {children}
    </svg>
  );
}

export const DownloadIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M12 3v12" />
    <path d="m7 10 5 5 5-5" />
    <path d="M5 21h14" />
  </Stroke>
);

export const CodeIcon = (p: IconProps) => (
  <Stroke {...p}>
    <polyline points="16 18 22 12 16 6" />
    <polyline points="8 6 2 12 8 18" />
  </Stroke>
);

export const CheckIcon = (p: IconProps) => (
  <Stroke {...p}>
    <polyline points="20 6 9 17 4 12" />
  </Stroke>
);

export const ArrowRightIcon = (p: IconProps) => (
  <Stroke {...p}>
    <line x1="5" y1="12" x2="19" y2="12" />
    <polyline points="12 5 19 12 12 19" />
  </Stroke>
);

export const MicIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="9" y="3" width="6" height="11" rx="3" />
    <path d="M6 11a6 6 0 0 0 12 0" />
    <path d="M12 17v4" />
  </Stroke>
);

export const MicOffIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="9" y="3" width="6" height="11" rx="3" />
    <path d="M6 11a6 6 0 0 0 12 0" />
    <line x1="4" y1="3.5" x2="20" y2="20.5" />
  </Stroke>
);

export const HeadphonesIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M4 14v-2a8 8 0 0 1 16 0v2" />
    <rect x="3" y="14" width="4" height="6" rx="1.5" />
    <rect x="17" y="14" width="4" height="6" rx="1.5" />
  </Stroke>
);

export const VolumeIcon = (p: IconProps) => (
  <Stroke {...p}>
    <polygon points="11 5 6 9 3 9 3 15 6 15 11 19 11 5" />
    <path d="M15.5 8.5a5 5 0 0 1 0 7" />
  </Stroke>
);

export const NoiseIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="9" y="3" width="6" height="11" rx="3" />
    <path d="M6 11a6 6 0 0 0 12 0" />
    <path d="M12 18v3" />
    <path d="m18 5 1.5-1.5" />
    <path d="M20 9h2" />
  </Stroke>
);

export const MessageIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M21 11.5a8.4 8.4 0 0 1-9 8.4L3 21l1.1-9A8.4 8.4 0 1 1 21 11.5z" />
  </Stroke>
);

export const UsersIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
  </Stroke>
);

export const UserIcon = (p: IconProps) => (
  <Stroke {...p}>
    <circle cx="12" cy="8" r="4" />
    <path d="M4 21v-1a6 6 0 0 1 12 0v1" />
  </Stroke>
);

export const LockIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="3" y="11" width="18" height="11" rx="2" />
    <path d="M7 11V7a5 5 0 0 1 10 0v4" />
  </Stroke>
);

export const ActivityIcon = (p: IconProps) => (
  <Stroke {...p}>
    <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
  </Stroke>
);

export const HashIcon = (p: IconProps) => (
  <Stroke {...p}>
    <line x1="4" y1="9" x2="20" y2="9" />
    <line x1="4" y1="15" x2="20" y2="15" />
    <line x1="10" y1="3" x2="8" y2="21" />
    <line x1="16" y1="3" x2="14" y2="21" />
  </Stroke>
);

export const ServerIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="2" y="3" width="20" height="8" rx="2" />
    <rect x="2" y="13" width="20" height="8" rx="2" />
    <line x1="6" y1="7" x2="6.01" y2="7" />
    <line x1="6" y1="17" x2="6.01" y2="17" />
  </Stroke>
);

export const KeyIcon = (p: IconProps) => (
  <Stroke {...p}>
    <circle cx="7.5" cy="15.5" r="4.5" />
    <path d="m10.5 12.5 8-8" />
    <path d="m15 5 3 3" />
    <path d="m18.5 1.5 3 3-3 3" />
  </Stroke>
);

export const CopyIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="9" y="9" width="11" height="11" rx="2" />
    <path d="M5 15V5a2 2 0 0 1 2-2h8" />
  </Stroke>
);

export const InfoIcon = (p: IconProps) => (
  <Stroke {...p}>
    <circle cx="12" cy="12" r="9" />
    <line x1="12" y1="11" x2="12" y2="16" />
    <line x1="12" y1="8" x2="12.01" y2="8" />
  </Stroke>
);

export const MonitorIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="2" y="3" width="20" height="14" rx="2" />
    <line x1="8" y1="21" x2="16" y2="21" />
    <line x1="12" y1="17" x2="12" y2="21" />
  </Stroke>
);

export const LogOutIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
    <polyline points="16 17 21 12 16 7" />
    <line x1="21" y1="12" x2="9" y2="12" />
  </Stroke>
);

export const ChevronDownIcon = (p: IconProps) => (
  <Stroke {...p}>
    <polyline points="6 9 12 15 18 9" />
  </Stroke>
);

export const CloseIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M18 6 6 18" />
    <path d="m6 6 12 12" />
  </Stroke>
);

export const AlertTriangleIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
    <line x1="12" y1="9" x2="12" y2="13" />
    <line x1="12" y1="17" x2="12.01" y2="17" />
  </Stroke>
);

export const MailIcon = (p: IconProps) => (
  <Stroke {...p}>
    <rect x="2" y="4" width="20" height="16" rx="2" />
    <path d="m22 6-10 7L2 6" />
  </Stroke>
);

/** Windows 四窗格标志（填充）。 */
export const WindowsIcon = ({ size = 24, ...props }: IconProps) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="currentColor"
    aria-hidden="true"
    {...props}
  >
    <path d="M3 5.5 10.5 4.5v7H3zM11.5 4.35 21 3v8.5h-9.5zM3 12.5h7.5v7L3 18.5zM11.5 12.5H21V21l-9.5-1.35z" />
  </svg>
);

/** macOS 苹果标志（填充）。 */
export const AppleIcon = ({ size = 24, ...props }: IconProps) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="currentColor"
    aria-hidden="true"
    {...props}
  >
    <path d="M16.4 12.9c0-2 1.6-3 1.7-3-.9-1.4-2.4-1.5-2.9-1.6-1.2-.1-2.4.7-3 .7s-1.6-.7-2.6-.7c-1.3 0-2.6.8-3.2 2-1.4 2.4-.4 6 1 8 .7 1 1.4 2 2.5 2 1 0 1.3-.6 2.5-.6s1.5.6 2.5.6 1.8-1 2.4-2c.8-1.1 1.1-2.2 1.1-2.3 0 0-2.2-.8-2.2-3.2zM14.6 6.9c.5-.7.9-1.6.8-2.6-.8 0-1.8.5-2.4 1.2-.5.6-1 1.6-.8 2.5.9.1 1.8-.4 2.4-1.1z" />
  </svg>
);

export const LinuxIcon = (p: IconProps) => (
  <Stroke {...p}>
    <path d="M12 2a4 4 0 0 0-4 4c0 1.5.5 2.5.5 4 0 2-2.5 3-2.5 6 0 2 2.5 4 6 4s6-2 6-4c0-3-2.5-4-2.5-6 0-1.5.5-2.5.5-4a4 4 0 0 0-4-4z" />
  </Stroke>
);
