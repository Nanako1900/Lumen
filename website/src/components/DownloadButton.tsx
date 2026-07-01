interface DownloadButtonProps {
  href: string | null;
  label?: string;
  disabled?: boolean;
}

/** 主下载按钮：指向 Setup.exe（href 为 null 时禁用）。 */
export default function DownloadButton({
  href,
  label = "下载 Windows 客户端",
  disabled = false,
}: DownloadButtonProps) {
  const base =
    "inline-flex items-center justify-center rounded-xl px-6 py-3 text-base font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400";

  if (!href || disabled) {
    return (
      <span
        aria-disabled="true"
        className={`${base} cursor-not-allowed bg-zinc-800 text-zinc-500`}
      >
        {label}
      </span>
    );
  }

  return (
    <a href={href} className={`${base} bg-indigo-600 text-white hover:bg-indigo-500`}>
      {label}
    </a>
  );
}
