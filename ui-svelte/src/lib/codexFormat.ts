export interface WindowSnapshot {
  used_percent: number;
  reset_after_seconds: number;
  reset_at: number;
}

export function formatResetSeconds(seconds: number): string {
  if (seconds <= 0) return "now";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function formatCacheRate(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

export function labelForWindow(window: WindowSnapshot): string {
  let label = `${window.used_percent.toFixed(1)}% used, resets in ${formatResetSeconds(window.reset_after_seconds)}`;
  if (window.reset_at > 0) {
    const d = new Date(window.reset_at * 1000);
    label += ` (${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")})`;
  }
  return label;
}
