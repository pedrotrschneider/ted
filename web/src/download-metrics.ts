export function downloadMetrics(speed: number | null, seconds: number | null): string {
  if (speed === null || seconds === null) return 'Measuring download speed… Estimated time remaining unavailable.';
  const minutes = Math.ceil(seconds / 60);
  const remaining = seconds < 60 ? 'less than a minute' : minutes < 60 ? `${minutes} min` : `${Math.floor(minutes / 60)} h${minutes % 60 ? ` ${minutes % 60} min` : ''}`;
  return `${speed.toLocaleString(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 })} Mbps · About ${remaining} remaining`;
}
