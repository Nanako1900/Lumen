/** 轻量格式化工具。 */

/** ISO 日期 → 本地化日期串；无效返回空串。 */
export function formatDate(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
  });
}

/** 版本号加 v 前缀（若尚无）。 */
export function formatVersion(version: string | undefined): string {
  if (!version) return "";
  return version.startsWith("v") ? version : `v${version}`;
}
