import { useCallback, useMemo } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Tooltip } from 'antd';
import {
  CloudServerOutlined,
  MoonFilled,
  MoonOutlined,
  SunOutlined,
  ThunderboltOutlined,
  UploadOutlined,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { pauseAnimationsUntilLeave, useTheme } from '@/hooks/useTheme';
import './TopStatusBar.css';

/** Xray state as /panel/api/server/status reports it. */
interface ServerStatusSlice {
  xray?: { state?: string; version?: string };
}

/** Node entry as the embedded VPNGate engine reports it. */
interface VpnGateNode {
  id?: string;
  country?: string;
  country_short?: string;
  latency_ms?: number;
  ping?: number;
  active?: boolean;
}

interface VpnGateNodesPayload {
  nodes?: VpnGateNode[];
  state?: {
    active_openvpn_node_id?: string;
    active_node_latency?: string | number;
    is_connecting?: boolean;
    proxy_ok?: boolean;
    proxy_ip?: string;
    proxy_latency_ms?: number;
    connection_enabled?: boolean;
  };
}

interface VersionPayload {
  currentVersion?: string;
  latestVersion?: string;
  updateAvailable?: boolean;
}

/**
 * The one status capsule LxGate keeps in front of every page: Xray's state, the
 * live VPNGate exit (country + latency), the light/dark toggle and the update
 * badge. Each block is a link to the page that owns it, so the capsule doubles as
 * a shortcut rail.
 *
 * Polling is deliberately slow (10s) and silent: the capsule must never be the
 * reason a page feels busy, and the dashboard's own 2s status query already
 * warms the same cache key when the operator is looking at it.
 */
export default function TopStatusBar() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { isDark, isUltra, toggleTheme, toggleUltra } = useTheme();

  const statusQuery = useQuery({
    queryKey: ['topbar', 'server-status'],
    queryFn: async (): Promise<ServerStatusSlice> => {
      const msg = await HttpUtil.get<ServerStatusSlice>('/panel/api/server/status', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
    staleTime: 5_000,
    retry: false,
  });

  const vpngateQuery = useQuery({
    queryKey: ['topbar', 'vpngate'],
    queryFn: async (): Promise<VpnGateNodesPayload> => {
      const msg = await HttpUtil.get<VpnGateNodesPayload>('/panel/api/vpngate/nodes', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
    staleTime: 5_000,
    retry: false,
  });

  const versionQuery = useQuery({
    queryKey: ['topbar', 'version'],
    queryFn: async (): Promise<VersionPayload> => {
      const msg = await HttpUtil.get<VersionPayload>('/panel/api/system/version', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: 30 * 60_000,
    staleTime: 10 * 60_000,
    retry: false,
  });

  const xrayRunning = statusQuery.data?.xray?.state === 'running';
  const xrayState = statusQuery.data?.xray?.state ?? 'unknown';

  const exit = useMemo(() => {
    const payload = vpngateQuery.data;
    if (!payload) return { label: t('pages.vpngate.statusUnknown'), tone: 'warn' as const };
    const state = payload.state ?? {};
    const activeID = state.active_openvpn_node_id ?? '';
    const nodes = payload.nodes ?? [];
    const activeNode = nodes.find((node) => node.id === activeID);
    if (state.is_connecting && !activeID) {
      return { label: t('pages.vpngate.connecting'), tone: 'warn' as const };
    }
    if (!activeID) {
      return {
        label: state.connection_enabled === false ? t('pages.vpngate.disconnected') : t('pages.vpngate.idle'),
        tone: 'warn' as const,
      };
    }
    const country = activeNode?.country || activeNode?.country_short || t('pages.vpngate.unknownCountry');
    const latency = Number(
      activeNode?.latency_ms || activeNode?.ping || state.active_node_latency || 0,
    );
    const suffix = latency > 0 ? ` · ${Math.round(latency)} ms` : '';
    return {
      label: `${country}${suffix}`,
      tone: state.proxy_ok === false ? ('bad' as const) : ('ok' as const),
    };
  }, [vpngateQuery.data, t]);

  const updateAvailable = !!versionQuery.data?.updateAvailable;
  const latestVersion = versionQuery.data?.latestVersion ?? '';

  // The panel's only theme switch: light → dark → ultra dark → light. The
  // sidebar used to carry a second copy of this cycle; with that gone this
  // button has to reach every palette, not just the two-way light/dark flip.
  const handleTheme = useCallback(() => {
    pauseAnimationsUntilLeave('topbar-theme');
    if (!isDark) {
      toggleTheme();
    } else if (!isUltra) {
      toggleUltra();
    } else {
      toggleUltra();
      toggleTheme();
    }
  }, [isDark, isUltra, toggleTheme, toggleUltra]);

  const themeLabel = isUltra
    ? t('pages.vpngate.toLight')
    : isDark
      ? t('pages.vpngate.toUltra')
      : t('pages.vpngate.toDark');

  return (
    <div className="lg-topbar" role="region" aria-label={t('pages.vpngate.statusCapsule')}>
      <div className="lg-topbar-group">
        <Tooltip title={t('pages.vpngate.xrayStatusHint')}>
          <button
            type="button"
            className={`lg-capsule ${xrayRunning ? 'ok' : 'bad'}`}
            onClick={() => navigate('/')}
          >
            <span className="lg-dot" aria-hidden />
            <CloudServerOutlined aria-hidden />
            <span className="lg-capsule-label">Xray</span>
            <span className="lg-capsule-value">{xrayState}</span>
          </button>
        </Tooltip>

        <Tooltip title={t('pages.vpngate.exitHint')}>
          <button
            type="button"
            className={`lg-capsule ${exit.tone}`}
            onClick={() => navigate('/vpngate')}
          >
            <span className="lg-dot" aria-hidden />
            <ThunderboltOutlined aria-hidden />
            <span className="lg-capsule-label">VPNGate</span>
            <span className="lg-capsule-value lg-num">{exit.label}</span>
          </button>
        </Tooltip>

        {updateAvailable && (
          <Tooltip title={t('pages.system.updateAvailableHint', { version: latestVersion })}>
            <button
              type="button"
              className="lg-capsule accent"
              onClick={() => navigate('/system')}
            >
              <UploadOutlined aria-hidden />
              <span className="lg-capsule-value">
                {latestVersion ? `v${latestVersion}` : t('pages.system.updateAvailable')}
              </span>
            </button>
          </Tooltip>
        )}
      </div>

      <div className="lg-topbar-group">
        <Tooltip title={themeLabel}>
          <Button
            id="topbar-theme"
            type="text"
            className="lg-topbar-icon"
            aria-label={themeLabel}
            icon={isUltra ? <SunOutlined /> : isDark ? <MoonFilled /> : <MoonOutlined />}
            onClick={handleTheme}
          />
        </Tooltip>
      </div>
    </div>
  );
}
