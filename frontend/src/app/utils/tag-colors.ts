/**
 * Colours for channel tags. The palette is what the manage screen offers
 * first; any #rrggbb is accepted (the "custom" swatch).
 */
export const TAG_PALETTE: { color: string; name: string }[] = [
  { color: '#e53935', name: 'אדום' },
  { color: '#f4511e', name: 'כתום' },
  { color: '#f9a825', name: 'צהוב' },
  { color: '#7cb342', name: 'ירוק בהיר' },
  { color: '#2e7d32', name: 'ירוק' },
  { color: '#00897b', name: 'טורקיז' },
  { color: '#0288d1', name: 'תכלת' },
  { color: '#1e40af', name: 'כחול' },
  { color: '#5e35b1', name: 'סגול' },
  { color: '#c2185b', name: 'ורוד' },
  { color: '#6d4c41', name: 'חום' },
  { color: '#546e7a', name: 'אפור' },
];

/**
 * Text colour that stays readable on a solid `hex` background: white on
 * dark and saturated colours, near-black on light ones (yellow, light green).
 */
export function tagInk(hex: string | undefined): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex || '');
  if (!m) return '#ffffff';
  const n = parseInt(m[1], 16);
  const lin = (c: number) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  };
  const L = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
  // Contrast against white vs against #1a1a1a (L ≈ 0.0103).
  return (1.05 / (L + 0.05)) >= ((L + 0.05) / 0.0603) ? '#ffffff' : '#1a1a1a';
}
