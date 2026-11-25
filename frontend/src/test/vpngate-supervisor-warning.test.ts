import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import type { TFunction } from 'i18next';

import { localizeSupervisorWarning } from '@/pages/vpngate/supervisorWarning';

const t = ((key: string) => `t:${key}`) as unknown as TFunction;

describe('localizeSupervisorWarning', () => {
  it('translates the openvpn-missing warning the Go supervisor emits', () => {
    // Keep in sync with the literal in internal/vpngate/config.go.
    const goSource = readFileSync(
      resolve(process.cwd(), '..', 'internal/vpngate/config.go'),
      'utf-8',
    );
    const match = goSource.match(/warning = "(openvpn is not installed[^"]*)"/);
    expect(match).not.toBeNull();
    expect(localizeSupervisorWarning(match![1], t)).toBe('t:pages.vpngate.openvpnMissing');
  });

  it('shows unknown warnings verbatim and empty ones as empty', () => {
    expect(localizeSupervisorWarning('something new happened', t)).toBe('something new happened');
    expect(localizeSupervisorWarning(undefined, t)).toBe('');
    expect(localizeSupervisorWarning('   ', t)).toBe('');
  });
});
