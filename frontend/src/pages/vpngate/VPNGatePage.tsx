import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  ConfigProvider,
  Empty,
  Input,
  Layout,
  Result,
  Segmented,
  Select,
  Space,
  Statistic,
  Switch,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApiOutlined,
  CheckCircleOutlined,
  CloudDownloadOutlined,
  DisconnectOutlined,
  LinkOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SettingOutlined,
  SwapOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import {
  useSetVpngateTakeover,
  useVpngateTakeoverQuery,
  type VpngateInboundOption,
  type VpngateTakeoverMode,
} from '@/api/queries/useVpngateTakeover';
import AppSidebar from '@/layouts/AppSidebar';
import TopStatusBar from '@/layouts/TopStatusBar';
import { setMessageInstance } from '@/utils/messageBus';
import './VPNGatePage.css';

type RoutingMode = 'auto' | 'fixed_region' | 'fixed_ip' | 'favorites';

interface VpnGateNode {
  id: string;
  country?: string;
  country_short?: string;
  host_name?: string;
  ip?: string;
  score?: number;
  ping?: number;
  speed?: number;
  sessions?: number;
  latency_ms?: number;
  proto?: string;
  remote_port?: number;
  probe_status?: string;
  probe_message?: string;
  active?: boolean;
  favorite?: boolean;
}

interface VpnGateState {
  active_openvpn_node_id?: string;
  active_node_latency?: string | number;
  is_connecting?: boolean;
  last_fetch_status?: string;
  last_check_message?: string;
  proxy_ok?: boolean;
  proxy_ip?: string;
  proxy_latency_ms?: number;
  proxy_error?: string;
  routing_mode?: RoutingMode;
  force_country?: string;
  routing_ip_type?: string;
  connection_enabled?: boolean;
  fixed_node_id?: string;
  favorite_node_ids?: string[];
  fav_fail_fallback?: boolean;
  target_valid_nodes?: number;
  blacklisted_nodes?: number;
}

interface NodesPayload {
  nodes?: VpnGateNode[];
  state?: VpnGateState;
}

interface ServiceLine {
  name?: string;
  status?: string;
  details?: string;
  error?: string;
}

interface SupervisorStatus {
  enabled?: boolean;
  available?: boolean;
  running?: boolean;
  state?: string;
  reason?: string;
  warning?: string;
  pid?: number;
  restarts?: number;
  apiAddress?: string;
  proxyAddress?: string;
  openvpnDev?: string;
  socksUrl?: string;
  httpProxyUrl?: string;
  uptimeSeconds?: number;
  dataDir?: string;
}

interface BindingState {
  tag?: string;
  bound?: boolean;
  proxyAddress?: string;
  socksUrl?: string;
  ruleExample?: string;
}

interface StatusPayload {
  supervisor?: SupervisorStatus;
  engine?: { services?: ServiceLine[] };
  engineError?: string;
  xray?: BindingState;
}

interface LogEntry {
  timestamp?: string;
  level?: string;
  module?: string;
  message?: string;
}

const MODE_VALUES: RoutingMode[] = ['auto', 'fixed_region', 'fixed_ip', 'favorites'];
const TAKEOVER_MODES: VpngateTakeoverMode[] = ['all', 'selected', 'manual'];

// Nodes are polled while the page is open: the engine re-tests candidates in the
// background and the table should follow without a manual refresh.
const NODES_POLL_MS = 4000;
const STATUS_POLL_MS = 8000;
const LOGS_POLL_MS = 4000;

function toneForNode(node: VpnGateNode): 'ok' | 'warn' | 'bad' {
  const latency = node.latency_ms || node.ping || 0;
  if (node.probe_status === 'failed') return 'bad';
  if (!latency) return 'warn';
  if (latency < 200) return 'ok';
  if (latency < 600) return 'warn';
  return 'bad';
}

/** The takeover draft the operator is editing, before it is applied. */
interface TakeoverDraft {
  mode: VpngateTakeoverMode;
  tags: string[];
}

function sameTags(a: string[], b: string[]) {
  if (a.length !== b.length) return false;
  const left = [...a].sort();
  const right = [...b].sort();
  return left.every((tag, index) => tag === right[index]);
}

export default function VPNGatePage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const queryClient = useQueryClient();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const [mode, setMode] = useState<RoutingMode>('auto');
  const [country, setCountry] = useState('');
  const [ipType, setIpType] = useState('all');
  const [favFallback, setFavFallback] = useState(true);
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [filter, setFilter] = useState('');
  const [draft, setDraft] = useState<TakeoverDraft | null>(null);
  const [draftDirty, setDraftDirty] = useState(false);

  const statusQuery = useQuery({
    queryKey: ['vpngate', 'status'],
    queryFn: async (): Promise<StatusPayload> => {
      const msg = await HttpUtil.get<StatusPayload>('/panel/api/vpngate/status', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: STATUS_POLL_MS,
    retry: false,
  });

  const nodesQuery = useQuery({
    queryKey: ['vpngate', 'nodes'],
    queryFn: async (): Promise<NodesPayload> => {
      const msg = await HttpUtil.get<NodesPayload>('/panel/api/vpngate/nodes', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    refetchInterval: NODES_POLL_MS,
    retry: false,
  });

  const logsQuery = useQuery({
    queryKey: ['vpngate', 'logs'],
    queryFn: async (): Promise<LogEntry[]> => {
      const msg = await HttpUtil.get<{ logs?: LogEntry[] }>('/panel/api/vpngate/logs', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj?.logs ?? []) : [];
    },
    refetchInterval: LOGS_POLL_MS,
    retry: false,
  });

  const takeoverQuery = useVpngateTakeoverQuery();
  const takeoverMutation = useSetVpngateTakeover();
  const takeover = takeoverQuery.data;

  const state = nodesQuery.data?.state ?? {};
  const nodes = nodesQuery.data?.nodes ?? [];
  const supervisor = statusQuery.data?.supervisor;
  const binding = statusQuery.data?.xray;
  const inbounds = takeover?.inbounds ?? [];

  // The engine is the source of truth for routing; seed the controls from it once
  // and then leave the operator's edits alone.
  const [seeded, setSeeded] = useState(false);
  useEffect(() => {
    if (seeded || !state.routing_mode) return;
    setMode(state.routing_mode);
    setCountry(state.force_country ?? '');
    setIpType(state.routing_ip_type || 'all');
    setFavFallback(state.fav_fail_fallback !== false);
    setSeeded(true);
  }, [
    state.routing_mode,
    state.force_country,
    state.routing_ip_type,
    state.fav_fail_fallback,
    seeded,
  ]);

  // The takeover draft mirrors the server until the operator touches it.
  useEffect(() => {
    if (!takeover || draftDirty) return;
    setDraft({ mode: takeover.mode, tags: takeover.inboundTags });
  }, [takeover, draftDirty]);

  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['vpngate'] });
    queryClient.invalidateQueries({ queryKey: ['topbar', 'vpngate'] });
  }, [queryClient]);

  const action = useMutation({
    mutationFn: async ({ url, body }: { url: string; body?: unknown }) => {
      const msg = await HttpUtil.post(url, body ?? {}, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg;
    },
    onSuccess: (_data, variables) => {
      messageApi.success(
        variables.url.includes('disconnect')
          ? t('pages.vpngate.toasts.disconnected')
          : t('pages.vpngate.toasts.actionDone'),
      );
      invalidate();
    },
    onError: (error: Error) => {
      messageApi.error(error.message);
      invalidate();
    },
  });

  const saveRouting = useCallback(() => {
    action.mutate({
      url: '/panel/api/vpngate/routing',
      body: { mode, country, ipType, favFallback },
    });
  }, [action, mode, country, ipType, favFallback]);

  const connectTo = useCallback(
    (nodeId: string) => action.mutate({ url: '/panel/api/vpngate/connect', body: { id: nodeId } }),
    [action],
  );

  const setTakeoverMode = useCallback(
    (next: VpngateTakeoverMode) => {
      setDraft((current) => ({
        mode: next,
        tags: current?.tags ?? takeover?.inboundTags ?? [],
      }));
      setDraftDirty(true);
    },
    [takeover],
  );

  const toggleTakeoverTag = useCallback((tag: string, on: boolean) => {
    setDraftDirty(true);
    setDraft((current) => {
      const base = current ?? { mode: 'selected' as VpngateTakeoverMode, tags: [] };
      const tags = on
        ? Array.from(new Set([...base.tags, tag]))
        : base.tags.filter((value) => value !== tag);
      return { mode: base.mode === 'manual' ? 'selected' : base.mode, tags };
    });
  }, []);

  const applyTakeover = useCallback(() => {
    if (!draft) return;
    takeoverMutation.mutate(
      { mode: draft.mode, inboundTags: draft.mode === 'all' ? [] : draft.tags },
      {
        onSuccess: (saved) => {
          setDraftDirty(false);
          setDraft({ mode: saved.mode, tags: saved.inboundTags });
          messageApi.success(t('pages.vpngate.takeover.applied'));
          invalidate();
        },
        onError: (error: Error) => {
          messageApi.error(error.message);
          invalidate();
        },
      },
    );
  }, [draft, takeoverMutation, messageApi, t, invalidate]);

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

  const columns = useMemo<ColumnsType<VpnGateNode>>(
    () => [
      {
        title: t('pages.vpngate.columns.country'),
        dataIndex: 'country',
        width: 150,
        render: (_value, node) => (
          <Space size={6}>
            <Tag bordered={false} className="vg-country-tag">
              {(node.country_short || '??').toUpperCase()}
            </Tag>
            <span>{node.country || '—'}</span>
          </Space>
        ),
      },
      {
        title: t('pages.vpngate.columns.host'),
        dataIndex: 'host_name',
        ellipsis: true,
        render: (_value, node) => (
          <div className="vg-host">
            <span className="lg-mono">{node.ip || node.host_name || '—'}</span>
            {node.active && (
              <Tag color="green" bordered={false}>
                {t('pages.vpngate.columns.active')}
              </Tag>
            )}
          </div>
        ),
      },
      {
        title: t('pages.vpngate.columns.latency'),
        dataIndex: 'latency_ms',
        width: 110,
        sorter: (a, b) => (a.latency_ms || a.ping || 0) - (b.latency_ms || b.ping || 0),
        defaultSortOrder: 'ascend',
        render: (_value, node) => {
          const latency = node.latency_ms || node.ping || 0;
          return (
            <span className={`vg-latency ${toneForNode(node)}`}>
              {latency > 0 ? `${Math.round(latency)} ms` : '—'}
            </span>
          );
        },
      },
      {
        title: t('pages.vpngate.columns.score'),
        dataIndex: 'score',
        width: 90,
        sorter: (a, b) => (a.score || 0) - (b.score || 0),
        render: (value: number) => <span className="lg-num">{value || 0}</span>,
      },
      {
        title: t('pages.vpngate.columns.sessions'),
        dataIndex: 'sessions',
        width: 100,
        responsive: ['md'],
        render: (value: number) => <span className="lg-num">{value || 0}</span>,
      },
      {
        title: '',
        key: 'actions',
        width: 120,
        align: 'right',
        render: (_value, node) => (
          <Button
            size="small"
            type={node.active ? 'default' : 'primary'}
            icon={node.active ? <CheckCircleOutlined /> : <ThunderboltOutlined />}
            loading={action.isPending}
            onClick={() => connectTo(node.id)}
          >
            {node.active ? t('pages.vpngate.connected') : t('pages.vpngate.connect')}
          </Button>
        ),
      },
    ],
    [t, action.isPending, connectTo],
  );

  const logsText = useMemo(() => {
    const entries = logsQuery.data ?? [];
    if (entries.length === 0) return '';
    return entries
      .slice(-160)
      .map((entry) =>
        `[${entry.timestamp ?? ''}] ${entry.level ?? ''} ${entry.module ?? ''} ${entry.message ?? ''}`.trim(),
      )
      .join('\n');
  }, [logsQuery.data]);

  const activeNode = nodes.find((node) => node.id === state.active_openvpn_node_id);
  const exitLabel =
    activeNode?.country || activeNode?.country_short || t('pages.vpngate.unknownCountry');
  const exitLatency =
    activeNode?.latency_ms || activeNode?.ping || Number(state.active_node_latency) || 0;

  const pageClass = `vpngate-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();
  const unavailable = supervisor && (!supervisor.enabled || !supervisor.available);

  const draftMode = draft?.mode ?? takeover?.mode ?? 'manual';
  const draftTags = draft?.tags ?? [];
  const selectedTags = draftMode === 'all' ? inbounds.map((inbound) => inbound.tag) : draftTags;
  const takeoverPending =
    !!takeover &&
    (draftMode !== takeover.mode ||
      !sameTags(draftMode === 'all' ? [] : draftTags, takeover.inboundTags));

  const advancedItems = [
    {
      key: 'advanced',
      label: (
        <span className="vg-advanced-label">
          <SettingOutlined aria-hidden /> {t('pages.vpngate.takeover.advanced.title')}
        </span>
      ),
      children: (
        <div className="vg-advanced">
          <div className="vg-advanced-grid">
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.proxy')}</span>
              <span className="lg-mono">{binding?.socksUrl || supervisor?.socksUrl || '—'}</span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.device')}</span>
              <span className="lg-mono">{supervisor?.openvpnDev || 'tun0'}</span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.rule')}</span>
              <span className="lg-mono">
                {binding?.bound
                  ? `#${takeover?.ruleIndex ?? -1} · ${binding?.tag || 'vpngate-out'}`
                  : t('pages.vpngate.notBound')}
              </span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.engine')}</span>
              <span className="lg-mono">{supervisor?.apiAddress || '127.0.0.1:18787'}</span>
            </div>
          </div>

          <div className="vg-bind">
            <div>
              <h4 className="lg-title">{t('pages.vpngate.xrayBindTitle')}</h4>
              <p className="lg-subtitle">{t('pages.vpngate.xrayBindHint')}</p>
              {binding?.ruleExample && <pre className="lg-log vg-rule">{binding.ruleExample}</pre>}
            </div>
            <Space wrap>
              <Button
                type={binding?.bound ? 'default' : 'primary'}
                icon={<ApiOutlined />}
                loading={action.isPending}
                onClick={() => action.mutate({ url: '/panel/api/vpngate/xray-bind' })}
              >
                {binding?.bound
                  ? t('pages.vpngate.rebindOutbound')
                  : t('pages.vpngate.bindOutbound')}
              </Button>
              <Button
                icon={<PoweroffOutlined />}
                disabled={!binding?.bound}
                loading={action.isPending}
                onClick={() => action.mutate({ url: '/panel/api/vpngate/xray-unbind' })}
              >
                {t('pages.vpngate.unbindOutbound')}
              </Button>
            </Space>
          </div>
        </div>
      ),
    },
  ];

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <TopStatusBar />
          <Layout.Content className="content-area">
            {statusQuery.isLoading ? (
              <div className="lg-card">
                <Statistic title={t('loading')} value="…" />
              </div>
            ) : statusQuery.isError ? (
              <Result
                status="error"
                title={t('somethingWentWrong')}
                subTitle={(statusQuery.error as Error)?.message}
                extra={
                  <Button type="primary" onClick={() => statusQuery.refetch()}>
                    {t('refresh')}
                  </Button>
                }
              />
            ) : (
              <div className="vg-page lg-stack">
                {unavailable && (
                  <Alert
                    type="warning"
                    showIcon
                    message={t('pages.vpngate.unavailableTitle')}
                    description={
                      supervisor?.reason ||
                      supervisor?.warning ||
                      t('pages.vpngate.unavailableHint')
                    }
                  />
                )}
                {!unavailable && supervisor?.warning && (
                  <Alert type="info" showIcon message={supervisor.warning} />
                )}
                {statusQuery.data?.engineError && (
                  <Alert
                    type="warning"
                    showIcon
                    message={t('pages.vpngate.engineErrorTitle')}
                    description={statusQuery.data.engineError}
                  />
                )}

                <Card className="vg-takeover" title={t('pages.vpngate.takeover.title')}>
                  <div className="vg-takeover-head">
                    <Segmented
                      block={isMobile}
                      size={isMobile ? 'middle' : 'large'}
                      className="vg-takeover-modes"
                      value={draftMode}
                      onChange={(value) => setTakeoverMode(value as VpngateTakeoverMode)}
                      options={TAKEOVER_MODES.map((value) => ({
                        value,
                        label: t(`pages.vpngate.takeover.modes.${value}`),
                      }))}
                    />
                    <p className="lg-subtitle">{t(`pages.vpngate.takeover.hints.${draftMode}`)}</p>
                  </div>

                  {draftMode === 'selected' && (
                    <div className="vg-inbound-picker">
                      <div className="vg-inbound-head">
                        <span className="vg-inbound-count">
                          {t('pages.vpngate.takeover.selectedCount', {
                            count: draftTags.length,
                            total: inbounds.length,
                          })}
                        </span>
                        <Space size={8}>
                          <Button
                            size="small"
                            onClick={() => {
                              setDraftDirty(true);
                              setDraft({
                                mode: 'selected',
                                tags: inbounds.map((inbound) => inbound.tag),
                              });
                            }}
                            disabled={inbounds.length === 0}
                          >
                            {t('pages.vpngate.takeover.selectAll')}
                          </Button>
                          <Button
                            size="small"
                            onClick={() => {
                              setDraftDirty(true);
                              setDraft({ mode: 'selected', tags: [] });
                            }}
                            disabled={draftTags.length === 0}
                          >
                            {t('pages.vpngate.takeover.clearAll')}
                          </Button>
                        </Space>
                      </div>
                      {inbounds.length === 0 ? (
                        <Empty
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          description={t('pages.vpngate.takeover.inboundsEmpty')}
                        />
                      ) : (
                        <div className="vg-inbound-list">
                          {inbounds.map((inbound: VpngateInboundOption) => (
                            <label
                              key={inbound.tag || inbound.id}
                              className={`vg-inbound-item${draftTags.includes(inbound.tag) ? ' is-on' : ''}`}
                            >
                              <Checkbox
                                checked={draftTags.includes(inbound.tag)}
                                onChange={(event) =>
                                  toggleTakeoverTag(inbound.tag, event.target.checked)
                                }
                              />
                              <span className="vg-inbound-main">
                                <span className="vg-inbound-remark">
                                  {inbound.remark || inbound.tag || `#${inbound.id}`}
                                </span>
                                <span className="vg-inbound-meta">
                                  <Tag bordered={false} color="purple">
                                    {inbound.protocol}
                                  </Tag>
                                  <span className="lg-mono">:{inbound.port}</span>
                                  {inbound.enable === false && (
                                    <Tag bordered={false}>{t('disabled')}</Tag>
                                  )}
                                </span>
                              </span>
                            </label>
                          ))}
                        </div>
                      )}
                    </div>
                  )}

                  <div className="vg-takeover-foot">
                    <span className="lg-subtitle">
                      {draftMode === 'all'
                        ? t('pages.vpngate.takeover.modes.all')
                        : t('pages.vpngate.takeover.selectedCount', {
                            count: selectedTags.length,
                            total: inbounds.length,
                          })}
                    </span>
                    <Button
                      type="primary"
                      icon={<SwapOutlined />}
                      loading={takeoverMutation.isPending}
                      disabled={!draft || !takeoverPending}
                      onClick={applyTakeover}
                    >
                      {t('pages.vpngate.takeover.apply')}
                    </Button>
                  </div>
                </Card>

                <div className="vg-tiles">
                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.engine')}</span>
                    <div className="vg-tile-value">
                      <span
                        className={`lg-pill ${
                          supervisor?.running ? 'ok' : supervisor?.available ? 'warn' : 'bad'
                        }`}
                      >
                        <span className="lg-dot" aria-hidden />
                        {supervisor?.state || t('pages.vpngate.statusUnknown')}
                      </span>
                    </div>
                    <span className="vg-tile-foot lg-mono">
                      {t('pages.vpngate.takeover.advanced.device')}{' '}
                      {supervisor?.openvpnDev || 'tun0'}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.exit')}</span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${state.proxy_ok === false ? 'bad' : 'ok'}`}>
                        <span className="lg-dot" aria-hidden />
                        {state.active_openvpn_node_id ? exitLabel : t('pages.vpngate.disconnected')}
                      </span>
                    </div>
                    <span className="vg-tile-foot lg-num">
                      {state.proxy_ip && state.proxy_ip !== '-' ? state.proxy_ip : '—'}
                      {exitLatency > 0 ? ` · ${Math.round(exitLatency)} ms` : ''}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.takeover')}</span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${draftMode === 'manual' ? 'warn' : 'ok'}`}>
                        <span className="lg-dot" aria-hidden />
                        {t(`pages.vpngate.takeover.modes.${draftMode}`)}
                      </span>
                    </div>
                    <span className="vg-tile-foot">
                      {t('pages.vpngate.tiles.nodes', { count: nodes.length })}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.xray')}</span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${binding?.bound ? 'ok' : 'warn'}`}>
                        <span className="lg-dot" aria-hidden />
                        {binding?.bound
                          ? binding?.tag || 'vpngate-out'
                          : t('pages.vpngate.notBound')}
                      </span>
                    </div>
                    <span className="vg-tile-foot">
                      {t('pages.vpngate.tiles.restarts', { count: supervisor?.restarts ?? 0 })}
                    </span>
                  </div>
                </div>

                <Card
                  className="vg-routing"
                  title={t('pages.vpngate.routingTitle')}
                  extra={
                    <Space wrap>
                      <Tooltip title={t('pages.vpngate.refreshHint')}>
                        <Button
                          icon={<ReloadOutlined />}
                          loading={action.isPending}
                          onClick={() => action.mutate({ url: '/panel/api/vpngate/refresh' })}
                        >
                          {t('pages.vpngate.refreshNodes')}
                        </Button>
                      </Tooltip>
                      <Tooltip title={t('pages.vpngate.disconnectHint')}>
                        <Button
                          danger
                          icon={<DisconnectOutlined />}
                          loading={action.isPending}
                          onClick={() => action.mutate({ url: '/panel/api/vpngate/disconnect' })}
                        >
                          {t('pages.vpngate.disconnect')}
                        </Button>
                      </Tooltip>
                    </Space>
                  }
                >
                  <div className="vg-routing-grid">
                    <label className="vg-field">
                      <span className="vg-field-label">{t('pages.vpngate.mode')}</span>
                      <Segmented
                        block={isMobile}
                        value={mode}
                        onChange={(value) => setMode(value as RoutingMode)}
                        options={MODE_VALUES.map((value) => ({
                          value,
                          label: t(`pages.vpngate.modes.${value}`),
                        }))}
                      />
                    </label>

                    <label className="vg-field">
                      <span className="vg-field-label">{t('pages.vpngate.country')}</span>
                      <Select
                        showSearch
                        allowClear
                        disabled={mode !== 'fixed_region'}
                        value={country || undefined}
                        placeholder={t('pages.vpngate.countryPlaceholder')}
                        options={countries}
                        onChange={(value) => setCountry((value ?? '').toUpperCase())}
                        style={{ minWidth: 180 }}
                      />
                    </label>

                    <label className="vg-field">
                      <span className="vg-field-label">{t('pages.vpngate.ipType')}</span>
                      <Select
                        value={ipType}
                        onChange={setIpType}
                        style={{ minWidth: 150 }}
                        options={[
                          { value: 'all', label: t('pages.vpngate.ipTypes.all') },
                          { value: 'residential', label: t('pages.vpngate.ipTypes.residential') },
                          { value: 'hosting', label: t('pages.vpngate.ipTypes.hosting') },
                        ]}
                      />
                    </label>

                    <label className="vg-field vg-field-switch">
                      <span className="vg-field-label">{t('pages.vpngate.favFallback')}</span>
                      <Switch checked={favFallback} onChange={setFavFallback} />
                    </label>

                    <div className="vg-field vg-field-actions">
                      <Button
                        type="primary"
                        icon={<SwapOutlined />}
                        loading={action.isPending}
                        onClick={saveRouting}
                      >
                        {t('pages.vpngate.applyRouting')}
                      </Button>
                    </div>
                  </div>

                  <p className="lg-subtitle">{t('pages.vpngate.routingHint')}</p>
                </Card>

                <Card className="vg-advanced-card" styles={{ body: { paddingTop: 4 } }}>
                  <Collapse ghost items={advancedItems} bordered={false} />
                </Card>

                <Card
                  className="vg-nodes"
                  title={t('pages.vpngate.nodesTitle')}
                  extra={
                    <Space wrap>
                      <Input
                        allowClear
                        placeholder={t('pages.vpngate.filterPlaceholder')}
                        onChange={(event) => setFilter(event.target.value)}
                        prefix={<ThunderboltOutlined />}
                        size="small"
                        style={{ width: 180 }}
                      />
                      <Button
                        icon={<CloudDownloadOutlined />}
                        loading={action.isPending}
                        onClick={() =>
                          action.mutate({
                            url: '/panel/api/vpngate/test-nodes',
                            body: { ids: selectedRowKeys.map(String) },
                          })
                        }
                        disabled={selectedRowKeys.length === 0}
                      >
                        {t('pages.vpngate.testSelected', { count: selectedRowKeys.length })}
                      </Button>
                      <Button
                        icon={<LinkOutlined />}
                        loading={action.isPending}
                        onClick={() => action.mutate({ url: '/panel/api/vpngate/check' })}
                      >
                        {t('pages.vpngate.fullCheck')}
                      </Button>
                    </Space>
                  }
                >
                  <NodeTable
                    nodes={nodes}
                    columns={columns}
                    selectedRowKeys={selectedRowKeys}
                    onSelectionChange={setSelectedRowKeys}
                    filter={filter}
                    emptyText={
                      state.last_check_message ||
                      (state.is_connecting
                        ? t('pages.vpngate.nodesLoading')
                        : t('pages.vpngate.nodesEmpty'))
                    }
                  />
                </Card>

                <Card
                  className="vg-logs"
                  title={t('pages.vpngate.logsTitle')}
                  extra={
                    <Space>
                      <Button
                        size="small"
                        icon={<ReloadOutlined />}
                        onClick={() => logsQuery.refetch()}
                      >
                        {t('refresh')}
                      </Button>
                    </Space>
                  }
                >
                  {logsText ? (
                    <pre className="lg-log" aria-live="polite">
                      {logsText}
                    </pre>
                  ) : (
                    <Empty
                      image={Empty.PRESENTED_IMAGE_SIMPLE}
                      description={t('pages.vpngate.logsEmpty')}
                    />
                  )}
                </Card>
              </div>
            )}
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

/** Keeps table filtering local to the table instance without re-rendering the page. */
function NodeTable({
  nodes,
  columns,
  selectedRowKeys,
  onSelectionChange,
  filter,
  emptyText,
}: {
  nodes: VpnGateNode[];
  columns: ColumnsType<VpnGateNode>;
  selectedRowKeys: React.Key[];
  onSelectionChange: (keys: React.Key[]) => void;
  filter: string;
  emptyText: string;
}) {
  const filtered = useMemo(() => {
    const term = filter.trim().toLowerCase();
    if (!term) return nodes;
    return nodes.filter((node) =>
      [node.country, node.country_short, node.ip, node.host_name, node.id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(term)),
    );
  }, [nodes, filter]);

  return (
    <Table<VpnGateNode>
      id="vg-node-table"
      rowKey="id"
      size="middle"
      columns={columns}
      dataSource={filtered}
      pagination={{ pageSize: 12, showSizeChanger: false, hideOnSinglePage: true }}
      scroll={{ x: 720 }}
      rowSelection={{ selectedRowKeys, onChange: onSelectionChange }}
      loading={nodes.length === 0}
      locale={{ emptyText }}
      rowClassName={(node) => (node.active ? 'vg-row-active' : '')}
    />
  );
}
