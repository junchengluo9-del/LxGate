import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Select } from 'antd';
import { ArrowRightOutlined, DisconnectOutlined, ThunderboltOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import {
  postVpngateJson,
  useVpngateTakeoverQuery,
  type VpngateTakeoverMode,
} from '@/api/queries/useVpngateTakeover';

import './CurrentExitCard.css';

interface VpnGateNode {
  id: string;
  country?: string;
  country_short?: string;
  ip?: string;
  latency_ms?: number;
  ping?: number;
  active?: boolean;
}

interface VpnGateState {
  active_openvpn_node_id?: string;
  active_node_latency?: string | number;
  is_connecting?: boolean;
  proxy_ok?: boolean;
  proxy_ip?: string;
  proxy_latency_ms?: number;
  routing_mode?: string;
  force_country?: string;
  routing_ip_type?: string;
  fav_fail_fallback?: boolean;
}

interface NodesPayload {
  nodes?: VpnGateNode[];
  state?: VpnGateState;
}

const POLL_MS = 10_000;

/**
 * 「当前出口」— one card on Overview that answers the exit question without a
 * detour through the VPNGate page: is traffic leaving through the tunnel right
 * now, from where, which takeover mode is live, and quick switches for all three.
 */
export default function CurrentExitCard({ isMobile }: { isMobile: boolean }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [error, setError] = useState('');

  const nodesQuery = useQuery({
    queryKey: ['vpngate', 'nodes'],
    queryFn: async (): Promise<NodesPayload> => {
      const msg = await HttpUtil.get<NodesPayload>('/panel/api/vpngate/nodes', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: POLL_MS,
    refetchIntervalInBackground: false,
    staleTime: 4_000,
    retry: false,
  });

  const takeoverQuery = useVpngateTakeoverQuery();

  const state = nodesQuery.data?.state ?? {};
  const nodes = nodesQuery.data?.nodes ?? [];
  const activeNode = nodes.find((node) => node.id === state.active_openvpn_node_id);
  const connected = !!state.active_openvpn_node_id;

  const exitCountry = activeNode?.country || activeNode?.country_short || '';
  const exitIP = state.proxy_ip && state.proxy_ip !== '-' ? state.proxy_ip : activeNode?.ip || '';
  const latency =
    activeNode?.latency_ms ||
    activeNode?.ping ||
    Number(state.active_node_latency) ||
    state.proxy_latency_ms ||
    0;

  const countries = useMemo(() => {
    const seen = new Map<string, string>();
    for (const node of nodes) {
      const code = (node.country_short || '').toUpperCase();
      if (!code || seen.has(code)) continue;
      seen.set(code, node.country ? `${code} · ${node.country}` : code);
    }
    return Array.from(seen.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((a, b) => a.value.localeCompare(b.value));
  }, [nodes]);

  const action = useMutation({
    mutationFn: async ({ url, body }: { url: string; body?: unknown }) => {
      const msg = await postVpngateJson(url, body);
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg;
    },
    onSuccess: () => {
      setError('');
      queryClient.invalidateQueries({ queryKey: ['vpngate'] });
      queryClient.invalidateQueries({ queryKey: ['topbar', 'vpngate'] });
    },
    onError: (err: Error) => setError(err.message),
  });

  const mode = takeoverQuery.data?.mode ?? 'manual';
  const modeLabel = t(`pages.vpngate.takeover.modes.${mode}`);
  const tone =
    state.proxy_ok === false
      ? 'bad'
      : !connected && state.is_connecting
        ? 'warn'
        : connected
          ? 'ok'
          : 'idle';
  const exitName = connected
    ? [exitCountry, exitIP].filter(Boolean).join(' · ')
    : state.is_connecting
      ? t('pages.vpngate.connecting')
      : t('pages.vpngate.disconnected');

  const switchMode = (next: VpngateTakeoverMode) => {
    action.mutate({ url: '/panel/api/vpngate/takeover', body: { mode: next, inboundTags: [] } });
  };

  const switchCountry = (code: string) => {
    if (!code) return;
    action.mutate({
      url: '/panel/api/vpngate/routing',
      body: {
        mode: 'fixed_region',
        country: code,
        ipType: state.routing_ip_type || 'all',
        favFallback: state.fav_fail_fallback !== false,
      },
    });
  };

  return (
    <section
      className={`ov-exit-card tone-${tone}`}
      aria-label={t('pages.index.currentExit.title')}
    >
      <header className="ov-exit-head">
        <span className="ov-exit-label">{t('pages.index.currentExit.title')}</span>
        <span className={`lg-pill ${connected ? 'ok' : state.is_connecting ? 'warn' : 'bad'}`}>
          <span className="lg-dot" aria-hidden />
          {connected
            ? t('pages.vpngate.connected')
            : state.is_connecting
              ? t('pages.vpngate.connecting')
              : t('pages.vpngate.disconnected')}
        </span>
      </header>

      <div className="ov-exit-body">
        <div className="ov-exit-main">
          <div className="ov-exit-country">{exitName}</div>
          <div className="ov-exit-meta">
            {latency > 0 && <span className="lg-num">{Math.round(latency)} ms</span>}
            <span className="ov-exit-dot" aria-hidden>
              ·
            </span>
            <span>
              {t('pages.index.currentExit.mode')}: <strong>{modeLabel}</strong>
            </span>
          </div>
        </div>

        <div className="ov-exit-actions">
          {connected ? (
            <Button
              size={isMobile ? 'small' : 'middle'}
              icon={<DisconnectOutlined />}
              loading={action.isPending}
              onClick={() => action.mutate({ url: '/panel/api/vpngate/disconnect' })}
            >
              {t('pages.vpngate.disconnect')}
            </Button>
          ) : (
            <Button
              size={isMobile ? 'small' : 'middle'}
              type="primary"
              icon={<ThunderboltOutlined />}
              onClick={() => navigate('/vpngate')}
            >
              {t('pages.index.currentExit.connect')}
            </Button>
          )}
          <Button
            size={isMobile ? 'small' : 'middle'}
            icon={<ArrowRightOutlined />}
            onClick={() => navigate('/vpngate')}
          >
            {t('pages.index.currentExit.open')}
          </Button>
        </div>
      </div>

      <div className="ov-exit-switches">
        <div className="ov-exit-modes" role="group" aria-label={t('pages.index.currentExit.mode')}>
          {(['all', 'selected', 'manual'] as VpngateTakeoverMode[]).map((value) => (
            <button
              key={value}
              type="button"
              className={`ov-exit-mode${mode === value ? ' is-on' : ''}`}
              onClick={() => switchMode(value)}
              disabled={action.isPending || takeoverQuery.isError}
            >
              {t(`pages.vpngate.takeover.modes.${value}`)}
            </button>
          ))}
        </div>
        <Select
          size={isMobile ? 'small' : 'middle'}
          className="ov-exit-country-select"
          showSearch
          allowClear
          placeholder={t('pages.index.currentExit.country')}
          value={(state.force_country || '').toUpperCase() || undefined}
          options={countries}
          onChange={(value) => switchCountry((value ?? '').toUpperCase())}
          disabled={action.isPending || countries.length === 0}
          optionFilterProp="label"
          popupMatchSelectWidth={false}
        />
      </div>

      {error && <p className="ov-exit-error">{error}</p>}
    </section>
  );
}
