import type { Locale } from './i18n';

const localeTag: Record<Locale, string> = { en: 'en', de: 'de-DE' };

// API values remain stable (ISO timestamps, integer capacities, and EUR
// amounts). Formatting happens only at the presentation boundary.
export function formatDateTime(locale: Locale, value: string | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(localeTag[locale], { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

export function formatNumber(locale: Locale, value: number | undefined): string {
  return value === undefined ? '—' : new Intl.NumberFormat(localeTag[locale]).format(value);
}

export function formatCurrency(locale: Locale, value: number | undefined, currency = 'EUR'): string {
  if (value === undefined) return '—';
  return new Intl.NumberFormat(localeTag[locale], { style: 'currency', currency, currencyDisplay: 'symbol' }).format(value);
}
