/**
 * Shared storage formatting, used by both the channel-owner storage panel and
 * the super-admin one. They previously carried byte-identical copies that had
 * already drifted (one guarded zero as `bytes === 0`, the other as `!bytes`),
 * so a change to one screen's number formatting silently diverged from the
 * other. Centralised here so they cannot.
 */

/** Human-readable size. `!bytes` also covers NaN/undefined defensively. */
export function formatBytes(bytes: number): string {
  if (!bytes) return '0 B';
  const gb = bytes / (1024 ** 3);
  if (gb >= 1) return gb.toFixed(2) + ' GB';
  const mb = bytes / (1024 ** 2);
  if (mb >= 1) return mb.toFixed(1) + ' MB';
  return (bytes / 1024).toFixed(0) + ' KB';
}

/** Maps a storage level to a Nebular status colour. */
export function storageLevelStatus(level: string | undefined): string {
  if (level === 'critical') return 'danger';
  if (level === 'warning') return 'warning';
  return 'success';
}
