import type { TFunction } from 'i18next';

/*
 * The Go supervisor reports host warnings as fixed English sentences
 * (internal/vpngate/config.go). Known ones are mapped to a translation so a
 * Chinese panel does not show a lone English banner; anything else is shown
 * verbatim so a new warning is never hidden.
 */
const KNOWN_WARNINGS: Array<{ prefix: string; key: string }> = [
  { prefix: 'openvpn is not installed', key: 'pages.vpngate.openvpnMissing' },
];

export function localizeSupervisorWarning(
  warning: string | undefined | null,
  t: TFunction,
): string {
  const text = (warning ?? '').trim();
  if (!text) return '';
  const known = KNOWN_WARNINGS.find((entry) => text.startsWith(entry.prefix));
  return known ? t(known.key) : text;
}
