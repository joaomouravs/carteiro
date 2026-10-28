// Small formatting helpers shared by pages and components.

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
  ["second", 1_000],
];

const relative = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

// relativeTime says how far `iso` is from now: "3 minutes ago", "in 2 hours".
export function relativeTime(iso: string, now: number = Date.now()): string {
  const diff = Date.parse(iso) - now;
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return relative.format(Math.round(diff / ms), unit);
  }
  return "just now";
}

const absolute = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "medium", timeZone: "UTC" });

// absoluteTime is the exact time, in UTC so it reads the same for everyone
// and matches Carteiro's logs.
export function absoluteTime(iso: string): string {
  return `${absolute.format(new Date(iso))} UTC`;
}

export function duration(ms: number): string {
  if (ms < 1_000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1_000).toFixed(ms < 10_000 ? 2 : 1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1_000)} s`;
}

// shortID keeps the end of a UUIDv7: its start is a timestamp, the same for
// everything created in the same millisecond, while the end is random.
export function shortID(id: string): string {
  return id.slice(-8);
}
