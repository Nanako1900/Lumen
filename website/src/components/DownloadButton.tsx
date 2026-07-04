import { buttonClass, type ButtonSize } from "./ui/button";
import { DownloadIcon } from "./icons";

interface DownloadButtonProps {
  href: string | null;
  label?: string;
  size?: ButtonSize;
  disabled?: boolean;
}

/** 主下载按钮：指向 Setup.exe（href 为 null 时禁用）。 */
export default function DownloadButton({
  href,
  label = "下载 Windows 客户端",
  size = "lg",
  disabled = false,
}: DownloadButtonProps) {
  if (!href || disabled) {
    return (
      <span
        aria-disabled="true"
        className={buttonClass(
          "secondary",
          size,
          "cursor-not-allowed opacity-60 shadow-none",
        )}
      >
        <DownloadIcon size={17} />
        {label}
      </span>
    );
  }

  return (
    <a href={href} className={buttonClass("primary", size)}>
      <DownloadIcon size={17} />
      {label}
    </a>
  );
}
