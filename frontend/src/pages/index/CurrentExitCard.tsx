import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { Button } from 'antd';
import { ArrowRightOutlined } from '@ant-design/icons';

import { useVpngateExitsQuery } from '@/api/queries/useVpngateExits';

import './CurrentExitCard.css';

/**
 * 「当前出口」— one read-only strip on Overview that answers the exit question
 * without a detour: which residential exits are carrying traffic right now, from
 * where, how fast, and how many inbounds each one takes.
 *
 * Two rules come from the page audit:
 *  - It reads the same `/panel/api/vpngate/exits` cache entry as the status bar
 *    (useVpngateExitsQuery), never the legacy single-exit `nodes` state. That
 *    state is cleared and rebuilt while the engine re-tests nodes, so a card fed
 *    from it flipped to "disconnected" for 10-20 s after every refresh, next to
 *    a status bar reading "2 exits online".
 *  - It writes nothing. The takeover switch here posted `inboundTags: []`, which
 *    unbound every inbound and restarted Xray; the exit page is the one place
 *    that changes traffic. This card only points there.
 */
export default function CurrentExitCard({ isMobile }: { isMobile: boolean }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const exitsQuery = useVpngateExitsQuery();

  const payload = exitsQuery.data;
  const exits = payload?.exits ?? [];
  const bindings = payload?.bindings ?? {};
  const live = exits.filter((exit) => exit.running);
  const connecting = exits.filter((exit) => exit.connecting && !exit.running);

  // Red means "an exit that should carry traffic is down". A fresh install with
  // no exit asked to connect is simply not set up yet: neutral, with a pointer.
  // An exit only "should be up" once it has carried a node or reported a
  // failure; the default slot of a fresh install has done neither.
  const wantsTraffic = exits.some(
    (exit) => !!exit.lastError || !!exit.nodeId || !!exit.connectedAt,
  );
  const notConfigured =
    !exitsQuery.isPending &&
    !exitsQuery.isError &&
    live.length === 0 &&
    connecting.length === 0 &&
    !wantsTraffic;

  const boundFor = (slot: number) =>
    Object.values(bindings).filter((boundSlot) => boundSlot === slot).length;

  const tone = exitsQuery.isPending
    ? 'idle'
    : live.length > 0
      ? 'ok'
      : connecting.length > 0
        ? 'warn'
        : notConfigured
          ? 'idle'
          : 'bad';

  const pill = exitsQuery.isPending
    ? { tone: 'warn' as const, label: t('loading') }
    : live.length > 1
      ? { tone: 'ok' as const, label: t('pages.vpngate.exitsOnline', { count: live.length }) }
      : live.length === 1
        ? { tone: 'ok' as const, label: t('pages.vpngate.connected') }
        : connecting.length > 0
          ? { tone: 'warn' as const, label: t('pages.vpngate.connecting') }
          : notConfigured
            ? { tone: 'idle' as const, label: t('pages.index.currentExit.notConfigured') }
            : {
                tone: 'bad' as const,
                label: exitsQuery.isError
                  ? t('pages.vpngate.statusUnknown')
                  : t('pages.vpngate.disconnected'),
              };

  const emptyText = exitsQuery.isError
    ? t('pages.vpngate.statusUnknown')
    : notConfigured
      ? t('pages.index.currentExit.notConfiguredHint')
      : t('pages.index.currentExit.noExit');

  return (
    <section
      className={`ov-exit-card tone-${tone}`}
      aria-label={t('pages.index.currentExit.title')}
    >
      <header className="ov-exit-head">
        <span className="ov-exit-label">{t('pages.index.currentExit.title')}</span>
        <span className={`lg-pill ${pill.tone}`}>
          <span className="lg-dot" aria-hidden />
          {pill.label}
        </span>
      </header>

      <div className="ov-exit-rows">
        {exitsQuery.isPending ? (
          <p className="ov-exit-row is-muted" aria-live="off">
            {t('loading')}
          </p>
        ) : live.length === 0 && connecting.length === 0 ? (
          <p className="ov-exit-row is-muted">{emptyText}</p>
        ) : (
          <>
            {live.map((exit) => {
              const country =
                exit.node?.country || exit.node?.country_short || t('pages.vpngate.unknownCountry');
              const ip = exit.proxyIp || exit.node?.ip || '';
              const latency = Math.round(Number(exit.latencyMs || exit.node?.latency_ms || 0));
              const bound = boundFor(exit.slot);
              return (
                <div key={exit.id} className="ov-exit-row">
                  <span className="ov-exit-row-name">{exit.name}</span>
                  <span className="ov-exit-row-node lg-mono">
                    {country}
                    {ip ? ` · ${ip}` : ''}
                  </span>
                  {latency > 0 && <span className="ov-exit-row-latency">{latency} ms</span>}
                  <span className="ov-exit-row-bound">
                    {t('pages.index.currentExit.inbounds', { count: bound })}
                  </span>
                </div>
              );
            })}
            {connecting.length > 0 && (
              <p className="ov-exit-row is-muted">
                {t('pages.vpngate.connecting')} · {connecting.map((exit) => exit.name).join(', ')}
              </p>
            )}
          </>
        )}
      </div>

      <div className="ov-exit-actions">
        <Button
          type="primary"
          ghost
          size={isMobile ? 'small' : 'middle'}
          icon={<ArrowRightOutlined />}
          onClick={() => navigate('/vpngate')}
        >
          {t('pages.index.currentExit.open')}
        </Button>
      </div>
    </section>
  );
}
