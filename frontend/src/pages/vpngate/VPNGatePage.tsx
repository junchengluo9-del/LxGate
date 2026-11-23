import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
  CheckCircleOutlined,
  CloudDownloadOutlined,
  DisconnectOutlined,
  LinkOutlined,
  ReloadOutlined,
  SearchOutlined,
  SettingOutlined,
  SwapOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import {
  postVpngateJson,
  useSetVpngateTakeover,
  useVpngateTakeoverQuery,
  type VpngateInboundOption,
} from '@/api/queries/useVpngateTakeover';
import {
  useBindVpngateExit,
  useDisconnectVpngateExit,
  useVpngateExitsQuery,
  type VpngateExit,
} from '@/api/queries/useVpngateExits';
import ExitsPanel from '@/pages/vpngate/ExitsPanel';
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
  /** residential / mobile / hosting; empty when the engine has not looked it up yet. */
  ip_type?: string;
  active?: boolean;
  favorite?: boolean;
}

interface NodesPayload {
  nodes?: VpnGateNode[];
  state?: Record<string, unknown>;
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

interface StatusPayload {
  supervisor?: SupervisorStatus;
  engine?: { services?: ServiceLine[] };
  engineError?: string;
}

interface LogEntry {
  timestamp?: string;
  level?: string;
  module?: string;
  message?: string;
}

const MODE_VALUES: RoutingMode[] = ['auto', 'fixed_region', 'fixed_ip', 'favorites'];

/** Node list filters; every one of them narrows the table immediately. */
type IpTypeFilter = 'all' | 'residential' | 'hosting';
const IP_TYPE_FILTERS: IpTypeFilter[] = ['all', 'residential', 'hosting'];
const IP_TYPE_KEYS = ['residential', 'mobile', 'hosting'] as const;

const IP_TYPE_COLORS: Record<string, string> = {
  residential: 'green',
  mobile: 'geekblue',
  hosting: 'orange',
};

/** The engine's answer to a policy save; only `level` drives how it is shown. */
interface PolicyFeedback {
  code?: string;
  level?: 'success' | 'info' | 'warning';
  message?: string;
  node?: string;
}

function nodeIpType(node: VpnGateNode) {
  const value = (node.ip_type || '').toLowerCase();
  return (IP_TYPE_KEYS as readonly string[]).includes(value) ? value : 'unknown';
}

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
  const [ipTypeFilter, setIpTypeFilter] = useState<IpTypeFilter>('all');
  const [countryFilter, setCountryFilter] = useState('all');
  const [onlyAvailable, setOnlyAvailable] = useState(false);
  // The exit the whole page is about. Everything under the cards -- inbound
  // binding, status tiles, routing policy, the node table's connect target --
  // reads this one value, so the operator never configures "some other exit".
  const [selectedExitId, setSelectedExitId] = useState('exit-1');
  const [bindingTag, setBindingTag] = useState<string | null>(null);
  const [bulkBinding, setBulkBinding] = useState(false);

  // A poll that fails while the engine is busy reconnecting a tunnel must not
  // wipe the page (it used to fall back to {} and the node table emptied):
  // throw so the last good answer stays, and keep it as the next poll starts.
  const statusQuery = useQuery({
    queryKey: ['vpngate', 'status'],
    queryFn: async (): Promise<StatusPayload> => {
      const msg = await HttpUtil.get<StatusPayload>('/panel/api/vpngate/status', undefined, {
        silent: true,
      });
      if (!msg?.success) throw new Error(msg?.msg || 'status unavailable');
      return msg.obj ?? {};
    },
    refetchInterval: STATUS_POLL_MS,
    placeholderData: keepPreviousData,
    retry: 1,
  });

  const nodesQuery = useQuery({
    queryKey: ['vpngate', 'nodes'],
    queryFn: async (): Promise<NodesPayload> => {
      const msg = await HttpUtil.get<NodesPayload>('/panel/api/vpngate/nodes', undefined, {
        silent: true,
      });
      if (!msg?.success) throw new Error(msg?.msg || 'nodes unavailable');
      return msg.obj ?? {};
    },
    refetchInterval: NODES_POLL_MS,
    placeholderData: keepPreviousData,
    retry: 1,
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

  // The takeover rule still decides where the inbounds that are *not* bound to a
  // specific exit go, so the binding panel reads it to label them honestly.
  const takeoverQuery = useVpngateTakeoverQuery();
  const takeover = takeoverQuery.data;
  const takeoverMutation = useSetVpngateTakeover();
  // Multi-exit: one shared node pool, one exit card per slot. The panel reads
  // the same /exits payload the inbound form uses, so the exit names match.
  const exitsQuery = useVpngateExitsQuery();
  const exits = exitsQuery.data?.exits ?? [];
  const bindings = exitsQuery.data?.bindings ?? {};
  const registeredSlots = exitsQuery.data?.registeredSlots ?? [];

  const bindExit = useBindVpngateExit();
  const disconnectExit = useDisconnectVpngateExit();

  const notify = useCallback(
    (kind: 'success' | 'info' | 'warning' | 'error', text: string) => {
      if (kind === 'success') messageApi.success(text);
      else if (kind === 'info') messageApi.info(text);
      else if (kind === 'warning') messageApi.warning(text, 7);
      else messageApi.error(text);
    },
    [messageApi],
  );

  const state = nodesQuery.data?.state ?? {};
  const nodes = nodesQuery.data?.nodes ?? [];
  const supervisor = statusQuery.data?.supervisor;
  const inbounds: VpngateInboundOption[] = exitsQuery.data?.inbounds ?? takeover?.inbounds ?? [];

  const selectedExit = useMemo(
    () => exits.find((exit) => exit.id === selectedExitId) ?? exits[0],
    [exits, selectedExitId],
  );

  // The default exit is exit-1; once the list arrives -- or the selected exit is
  // deleted from its card -- the selection falls back to a card that exists.
  useEffect(() => {
    if (exits.length === 0) return;
    if (!exits.some((exit) => exit.id === selectedExitId)) {
      setSelectedExitId(exits[0].id);
    }
  }, [exits, selectedExitId]);

  const exitBySlot = useMemo(() => {
    const map = new Map<number, VpngateExit>();
    for (const exit of exits) map.set(exit.slot, exit);
    return map;
  }, [exits]);

  // Slot 1 is the @vpngate-out outbound, the one a global takeover rule targets.
  const takeoverExit = useMemo(() => {
    const ruleMatchesEveryInbound = takeover?.mode === 'all';
    const ruleMatchesSome = takeover?.mode === 'selected' && takeover.inboundTags.length > 0;
    return ruleMatchesEveryInbound || ruleMatchesSome ? exitBySlot.get(1) : undefined;
  }, [takeover, exitBySlot]);

  const seedCountry = useCallback(
    (raw?: string) => {
      const value = (raw ?? '').trim();
      if (!value) return '';
      const upper = value.toUpperCase();
      if (nodes.some((node) => (node.country_short || '').toUpperCase() === upper)) return upper;
      return nodes.find((node) => node.country === value)?.country_short?.toUpperCase() ?? value;
    },
    [nodes],
  );

  // The routing controls follow the selected exit. They are re-seeded when that
  // exit's saved policy changes on the server -- not on every poll, and never
  // while the operator is editing, which is what the signature guards.
  const policySignature = selectedExit
    ? JSON.stringify([
        selectedExit.id,
        selectedExit.routing?.routing_mode ?? 'auto',
        selectedExit.routing?.force_country ?? '',
        selectedExit.routing?.routing_ip_type ?? 'all',
        selectedExit.routing?.fav_fail_fallback !== false,
      ])
    : '';
  const seededSignature = useRef('');
  useEffect(() => {
    if (!selectedExit || !policySignature || seededSignature.current === policySignature) return;
    seededSignature.current = policySignature;
    const routing = selectedExit.routing ?? {};
    setMode((routing.routing_mode ?? 'auto') as RoutingMode);
    setCountry(seedCountry(routing.force_country));
    setIpType(routing.routing_ip_type ?? 'all');
    setFavFallback(routing.fav_fail_fallback !== false);
  }, [selectedExit, policySignature, seedCountry]);

  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['vpngate'] });
    queryClient.invalidateQueries({ queryKey: ['topbar', 'vpngate'] });
  }, [queryClient]);

  const action = useMutation({
    mutationFn: async ({ url, body }: { url: string; body?: unknown }) => {
      const msg = await postVpngateJson(url, body);
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg;
    },
    onSuccess: (data, variables) => {
      messageApi.success(
        variables.url.includes('disconnect')
          ? t('pages.vpngate.toasts.disconnected')
          : data?.msg || t('pages.vpngate.toasts.actionDone'),
      );
      invalidate();
    },
    onError: (error: Error) => {
      messageApi.error(error.message);
      invalidate();
    },
  });

  // Saving writes the policy of the *selected* exit only; the engine answers
  // with what that means for the live node (kept, switching, or nothing fits),
  // and that sentence is the toast.
  const savePolicy = useMutation({
    mutationFn: async () => {
      if (!selectedExit) throw new Error(t('pages.vpngate.exits.empty'));
      const msg = await postVpngateJson<PolicyFeedback>('/panel/api/vpngate/exits/update', {
        id: selectedExit.id,
        routing_mode: mode,
        force_country: country,
        routing_ip_type: ipType,
        fav_fail_fallback: favFallback,
      });
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg;
    },
    onSuccess: (msg) => {
      const feedback = (msg?.obj ?? {}) as PolicyFeedback;
      const text = msg?.msg || feedback.message || t('pages.vpngate.exits.saved');
      if (feedback.level === 'warning') messageApi.warning(text, 7);
      else if (feedback.level === 'info') messageApi.info(text, 6);
      else messageApi.success(text, 5);
      invalidate();
    },
    onError: (error: Error) => {
      messageApi.error(error.message);
      invalidate();
    },
  });

  // A node is connected *to an exit*: the table's button always means the exit
  // selected above, whose name it carries.
  const connectToExit = useCallback(
    (nodeId: string, exitId: string) => {
      action.mutate({
        url: '/panel/api/vpngate/exits/connect',
        body: { exitId, nodeId },
      });
    },
    [action],
  );

  const toggleBinding = useCallback(
    (inboundTag: string, on: boolean) => {
      if (!selectedExit || bindingTag) return;
      setBindingTag(inboundTag);
      bindExit.mutate(
        { inboundTag, exitId: on ? selectedExit.id : '' },
        {
          onSuccess: () =>
            messageApi.success(
              on
                ? t('pages.vpngate.takeover.boundToast', { name: selectedExit.name })
                : t('pages.vpngate.takeover.directToast'),
            ),
          onError: (error: Error) => messageApi.error(error.message),
          onSettled: () => setBindingTag(null),
        },
      );
    },
    [selectedExit, bindingTag, bindExit, messageApi, t],
  );

  // Each bind rewrites the Xray template and reloads Xray, so a batch is run one
  // inbound at a time rather than fanned out: parallel writes would race.
  const moveInbounds = useCallback(
    async (toSelected: boolean) => {
      if (!selectedExit || bulkBinding) return;
      const targets = inbounds.filter((inbound) =>
        toSelected
          ? bindings[inbound.tag] !== selectedExit.slot
          : bindings[inbound.tag] === selectedExit.slot,
      );
      if (targets.length === 0) return;
      setBulkBinding(true);
      let done = 0;
      for (const inbound of targets) {
        try {
          await bindExit.mutateAsync({
            inboundTag: inbound.tag,
            exitId: toSelected ? selectedExit.id : '',
          });
          done += 1;
        } catch (error) {
          messageApi.error((error as Error).message);
          break;
        }
      }
      setBulkBinding(false);
      if (done === 0) return;
      messageApi.success(
        toSelected
          ? t('pages.vpngate.takeover.movedToast', { count: done, name: selectedExit.name })
          : t('pages.vpngate.takeover.freedToast', { count: done }),
      );
    },
    [selectedExit, bulkBinding, inbounds, bindings, bindExit, messageApi, t],
  );

  const scrollToPolicy = useCallback((exitId: string) => {
    setSelectedExitId(exitId);
    window.setTimeout(() => {
      const card = document.getElementById('vg-routing');
      if (!card) return;
      card.scrollIntoView({ behavior: 'smooth', block: 'start' });
      card.classList.add('is-flash');
      window.setTimeout(() => card.classList.remove('is-flash'), 1400);
    }, 60);
  }, []);

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

  const exitOwners = useMemo(() => {
    const owners = new Map<string, { exitId: string; name: string }>();
    for (const exit of exits) {
      if (exit.running && exit.nodeId)
        owners.set(exit.nodeId, { exitId: exit.id, name: exit.name });
    }
    return owners;
  }, [exits]);

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
        title: t('pages.vpngate.columns.ipType'),
        dataIndex: 'ip_type',
        width: 108,
        // Deliberately not responsive: the IP type is the column the operator
        // asked for, so it stays on a phone even when other columns drop out.
        render: (_value, node) => {
          const key = nodeIpType(node);
          return (
            <Tag bordered={false} color={IP_TYPE_COLORS[key]} className={`vg-ip-type ${key}`}>
              {t(`pages.vpngate.ipTypeTags.${key}`)}
            </Tag>
          );
        },
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
                {exitOwners.get(node.id)?.name || t('pages.vpngate.columns.active')}
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
        width: 150,
        align: 'right',
        render: (_value, node) => {
          const owner = exitOwners.get(node.id);
          if (owner) {
            return (
              <Tooltip title={t('pages.vpngate.exits.occupiedBy', { name: owner.name })}>
                <Tag color="geekblue" bordered={false} className="vg-node-owner">
                  <CheckCircleOutlined aria-hidden /> {owner.name}
                </Tag>
              </Tooltip>
            );
          }
          if (!selectedExit) {
            return (
              <Button size="small" disabled>
                {t('pages.vpngate.connect')}
              </Button>
            );
          }
          // The row button carries the target exit's name rather than the verb:
          // "Connect" is already the table's whole job, and the long label was
          // the one thing that did not fit the column.
          return (
            <Tooltip title={t('pages.vpngate.exits.connectTo', { name: selectedExit.name })}>
              <Button
                size="small"
                type="primary"
                icon={<ThunderboltOutlined />}
                loading={action.isPending}
                onClick={() => connectToExit(node.id, selectedExit.id)}
              >
                {selectedExit.name}
              </Button>
            </Tooltip>
          );
        },
      },
    ],
    [t, action.isPending, connectToExit, exitOwners, selectedExit],
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

  const pageClass = `vpngate-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();
  const unavailable = supervisor && (!supervisor.enabled || !supervisor.available);

  const selectedNode = selectedExit?.node;
  const selectedCountry = selectedNode?.country || selectedNode?.country_short || '';
  const selectedIp = selectedExit?.proxyIp || selectedNode?.ip || '';
  const selectedLatency =
    selectedExit?.latencyMs || selectedNode?.latency_ms || 0;
  const boundHere = selectedExit
    ? inbounds.filter((inbound) => bindings[inbound.tag] === selectedExit.slot).length
    : 0;
  // A global takeover rule can still be carrying inbounds to the exit that owns
  // vpngate-out. Those are not per-inbound bindings, so the panel shows them
  // indeterminate rather than pretending they are (or are not) checked.
  const implicitHere =
    !!selectedExit && !!takeoverExit && takeoverExit.slot === selectedExit.slot;
  const outboundRegistered = !!selectedExit && registeredSlots.includes(selectedExit.slot);
  const exitTone = !selectedExit
    ? 'bad'
    : selectedExit.connecting
      ? 'warn'
      : selectedExit.running
        ? 'ok'
        : 'bad';
  const exitStateLabel = !selectedExit
    ? t('pages.vpngate.statusUnknown')
    : selectedExit.connecting
      ? t('pages.vpngate.connecting')
      : selectedExit.running
        ? t('pages.vpngate.connected')
        : t('pages.vpngate.disconnected');

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
              <span className="lg-mono">
                {selectedExit?.socksUrl || selectedExit?.proxyAddress || '—'}
              </span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.device')}</span>
              <span className="lg-mono">{selectedExit?.dev || '—'}</span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.rule')}</span>
              <span className="lg-mono">{selectedExit?.outboundTag || '—'}</span>
            </div>
            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.takeover.advanced.engine')}</span>
              <span className="lg-mono">{supervisor?.apiAddress || '127.0.0.1:18787'}</span>
            </div>
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

                <ExitsPanel
                  data={exitsQuery.data}
                  selectedId={selectedExit?.id ?? selectedExitId}
                  onSelect={setSelectedExitId}
                  onOpenPolicy={scrollToPolicy}
                  onToast={notify}
                  onError={(error) => messageApi.error(error.message)}
                />

                {/* Inbound binding: it is always the selected exit's panel, so the
                    title names the exit instead of asking which one is meant. */}
                <Card
                  id="vg-binding"
                  className="vg-takeover"
                  title={
                    <span className="vg-card-title">
                      {t('pages.vpngate.takeover.bindTitle')}
                      {selectedExit && (
                        <span className="vg-card-title-target"> · {selectedExit.name}</span>
                      )}
                    </span>
                  }
                  extra={
                    selectedExit && (
                      <Tag bordered={false} className="vg-exit-slot lg-mono">
                        {selectedExit.dev}
                      </Tag>
                    )
                  }
                >
                  <p className="lg-subtitle vg-bind-hint">
                    {selectedExit
                      ? t('pages.vpngate.takeover.bindHint', { name: selectedExit.name })
                      : t('pages.vpngate.exits.empty')}
                  </p>

                  <div className="vg-inbound-head">
                    <span className="vg-inbound-count">
                      {t('pages.vpngate.takeover.bindCount', {
                        bound: boundHere,
                        total: inbounds.length,
                      })}
                    </span>
                    <Space size={8} wrap>
                      <Button
                        size="small"
                        type="primary"
                        ghost
                        loading={bulkBinding}
                        disabled={!selectedExit || inbounds.length === 0}
                        onClick={() => void moveInbounds(true)}
                      >
                        {t('pages.vpngate.takeover.bindAll')}
                      </Button>
                      <Button
                        size="small"
                        loading={bulkBinding}
                        disabled={!selectedExit || boundHere === 0}
                        onClick={() => void moveInbounds(false)}
                      >
                        {t('pages.vpngate.takeover.bindDirect')}
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
                      {inbounds.map((inbound) => {
                        const here = !!selectedExit && bindings[inbound.tag] === selectedExit.slot;
                        const other = here
                          ? undefined
                          : exitBySlot.get(bindings[inbound.tag] ?? 0);
                        const carried = !here && !other && implicitHere;
                        return (
                          <label
                            key={inbound.tag || inbound.id}
                            className={`vg-inbound-item${here ? ' is-on' : ''}${other ? ' is-other' : ''}`}
                          >
                            <Checkbox
                              checked={here}
                              indeterminate={carried}
                              disabled={!selectedExit || bindingTag === inbound.tag || bulkBinding}
                              onChange={(event) =>
                                toggleBinding(inbound.tag, event.target.checked)
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
                                {here && (
                                  <Tag bordered={false} color="blue">
                                    {t('pages.vpngate.takeover.onThisExit')}
                                  </Tag>
                                )}
                                {other && (
                                  <Tag bordered={false} color="geekblue">
                                    {t('pages.vpngate.takeover.currentlyOn', { name: other.name })}
                                  </Tag>
                                )}
                                {!here && !other && takeoverExit && (
                                  <Tag bordered={false} color="orange">
                                    {t('pages.vpngate.takeover.viaTakeover', {
                                      name: takeoverExit.name,
                                    })}
                                  </Tag>
                                )}
                                {!here && !other && !takeoverExit && (
                                  <Tag bordered={false}>
                                    {t('pages.vpngate.takeover.direct')}
                                  </Tag>
                                )}
                                {inbound.enable === false && (
                                  <Tag bordered={false}>{t('disabled')}</Tag>
                                )}
                              </span>
                            </span>
                          </label>
                        );
                      })}
                    </div>
                  )}

                  <div className="vg-bind-foot">
                    <span className="lg-subtitle">
                      {takeoverExit
                        ? t('pages.vpngate.takeover.fallbackNote', { name: takeoverExit.name })
                        : t('pages.vpngate.takeover.directNote')}
                    </span>
                    {takeoverExit && (
                      <Button
                        size="small"
                        type="link"
                        loading={takeoverMutation.isPending}
                        onClick={() =>
                          takeoverMutation.mutate(
                            { mode: 'manual', inboundTags: [] },
                            {
                              onSuccess: () =>
                                messageApi.success(t('pages.vpngate.takeover.ruleCleared')),
                              onError: (error: Error) => messageApi.error(error.message),
                            },
                          )
                        }
                      >
                        {t('pages.vpngate.takeover.clearRule')}
                      </Button>
                    )}
                  </div>
                </Card>

                <div className="vg-tiles">
                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.engine')}</span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${exitTone}`}>
                        <span className="lg-dot" aria-hidden />
                        {exitStateLabel}
                      </span>
                    </div>
                    <span className="vg-tile-foot lg-mono">
                      {t('pages.vpngate.tiles.deviceLine', {
                        dev: selectedExit?.dev || '—',
                        table: selectedExit?.table ?? '—',
                      })}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.exit')}</span>
                    <div className="vg-tile-value">
                      <span
                        className={`lg-pill ${
                          selectedExit && selectedExit.proxyOk === false ? 'bad' : selectedIp ? 'ok' : 'warn'
                        }`}
                      >
                        <span className="lg-dot" aria-hidden />
                        {selectedCountry || t('pages.vpngate.disconnected')}
                      </span>
                    </div>
                    <span className="vg-tile-foot lg-num">
                      {selectedIp || '—'}
                      {selectedLatency > 0 ? ` · ${Math.round(selectedLatency)} ms` : ''}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">
                      {t('pages.vpngate.tiles.boundInbounds')}
                    </span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${boundHere > 0 ? 'ok' : 'warn'}`}>
                        <span className="lg-dot" aria-hidden />
                        <span className="lg-num vg-tile-count">{boundHere}</span>
                      </span>
                    </div>
                    <span className="vg-tile-foot">
                      {t('pages.vpngate.tiles.boundInboundsFoot', {
                        total: inbounds.length,
                      })}
                    </span>
                  </div>

                  <div className="lg-card vg-tile">
                    <span className="vg-tile-label">{t('pages.vpngate.tiles.xray')}</span>
                    <div className="vg-tile-value">
                      <span className={`lg-pill ${outboundRegistered ? 'ok' : 'warn'}`}>
                        <span className="lg-dot" aria-hidden />
                        {selectedExit?.outboundTag || 'vpngate-out'}
                      </span>
                    </div>
                    <span className="vg-tile-foot lg-mono">
                      {t('pages.vpngate.tiles.outboundFoot', {
                        port: selectedExit?.proxyPort ?? '—',
                      })}
                    </span>
                  </div>
                </div>

                <Card
                  id="vg-routing"
                  className="vg-routing"
                  title={
                    <span className="vg-card-title">
                      {t('pages.vpngate.routingTitle')}
                      {selectedExit && (
                        <span className="vg-card-title-target"> · {selectedExit.name}</span>
                      )}
                    </span>
                  }
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
                      <Tooltip
                        title={
                          selectedExit
                            ? t('pages.vpngate.exits.disconnectHint', { name: selectedExit.name })
                            : t('pages.vpngate.disconnectHint')
                        }
                      >
                        <Button
                          danger
                          icon={<DisconnectOutlined />}
                          loading={disconnectExit.isPending}
                          disabled={!selectedExit}
                          onClick={() => {
                            if (!selectedExit) return;
                            disconnectExit.mutate(selectedExit.id, {
                              onSuccess: () =>
                                messageApi.success(t('pages.vpngate.toasts.disconnected')),
                              onError: (error: Error) => messageApi.error(error.message),
                            });
                          }}
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
                        loading={savePolicy.isPending}
                        disabled={!selectedExit}
                        onClick={() => savePolicy.mutate()}
                      >
                        {t('pages.vpngate.exits.savePolicy')}
                      </Button>
                    </div>
                  </div>

                  <p className="lg-subtitle">{t('pages.vpngate.exits.policyHint')}</p>
                </Card>

                <Card className="vg-advanced-card" styles={{ body: { paddingTop: 4 } }}>
                  <Collapse ghost items={advancedItems} bordered={false} />
                </Card>

                <Card
                  className="vg-nodes"
                  title={t('pages.vpngate.nodesTitle')}
                  extra={
                    <Space wrap>
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
                  <div className="vg-nodes-target">
                    <SwapOutlined aria-hidden />
                    <span>
                      {selectedExit
                        ? t('pages.vpngate.exits.connectTarget', { name: selectedExit.name })
                        : t('pages.vpngate.exits.empty')}
                    </span>
                  </div>
                  <div className="vg-node-filters">
                    <Input
                      allowClear
                      placeholder={t('pages.vpngate.filterPlaceholder')}
                      onChange={(event) => setFilter(event.target.value)}
                      prefix={<SearchOutlined />}
                      className="vg-node-search"
                    />
                    <Select<IpTypeFilter>
                      value={ipTypeFilter}
                      onChange={setIpTypeFilter}
                      className="vg-node-filter"
                      options={IP_TYPE_FILTERS.map((value) => ({
                        value,
                        label:
                          value === 'all'
                            ? t('pages.vpngate.ipTypeFilter.all')
                            : value === 'residential'
                              ? t('pages.vpngate.ipTypeFilter.residential')
                              : t('pages.vpngate.ipTypeFilter.hosting'),
                      }))}
                    />
                    <Select
                      showSearch
                      optionFilterProp="label"
                      value={countryFilter}
                      onChange={setCountryFilter}
                      className="vg-node-filter"
                      options={[
                        { value: 'all', label: t('pages.vpngate.countryFilter.all') },
                        ...countries,
                      ]}
                    />
                    <label className="vg-node-toggle">
                      <Switch size="small" checked={onlyAvailable} onChange={setOnlyAvailable} />
                      <span>{t('pages.vpngate.onlyAvailable')}</span>
                    </label>
                  </div>
                  <NodeTable
                    nodes={nodes}
                    columns={columns}
                    selectedRowKeys={selectedRowKeys}
                    onSelectionChange={setSelectedRowKeys}
                    filter={filter}
                    ipTypeFilter={ipTypeFilter}
                    countryFilter={countryFilter}
                    onlyAvailable={onlyAvailable}
                    emptyText={
                      (state.last_check_message as string | undefined) ||
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
  ipTypeFilter,
  countryFilter,
  onlyAvailable,
  emptyText,
}: {
  nodes: VpnGateNode[];
  columns: ColumnsType<VpnGateNode>;
  selectedRowKeys: React.Key[];
  onSelectionChange: (keys: React.Key[]) => void;
  filter: string;
  ipTypeFilter: IpTypeFilter;
  countryFilter: string;
  onlyAvailable: boolean;
  emptyText: string;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);

  // A narrowed list must start at its first page, otherwise the table shows an
  // empty page 3 while the matches sit on page 1.
  useEffect(() => {
    setPage(1);
  }, [filter, ipTypeFilter, countryFilter, onlyAvailable]);

  const filtered = useMemo(() => {
    const term = filter.trim().toLowerCase();
    return nodes.filter((node) => {
      if (onlyAvailable && node.probe_status !== 'available' && !node.active) return false;
      if (countryFilter !== 'all' && (node.country_short || '').toUpperCase() !== countryFilter) {
        return false;
      }
      if (ipTypeFilter !== 'all') {
        const type = nodeIpType(node);
        if (ipTypeFilter === 'residential' && type !== 'residential' && type !== 'mobile')
          return false;
        if (ipTypeFilter === 'hosting' && type !== 'hosting') return false;
      }
      if (!term) return true;
      return [node.country, node.country_short, node.ip, node.host_name, node.ip_type, node.id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(term));
    });
  }, [nodes, filter, ipTypeFilter, countryFilter, onlyAvailable]);

  return (
    <>
      <div className="vg-node-count lg-subtitle">
        {filtered.length === nodes.length
          ? t('pages.vpngate.filteredCountAll', { total: nodes.length })
          : t('pages.vpngate.filteredCount', { count: filtered.length, total: nodes.length })}
      </div>
      <Table<VpnGateNode>
        id="vg-node-table"
        rowKey="id"
        size="middle"
        columns={columns}
        dataSource={filtered}
        pagination={{
          current: page,
          pageSize: 12,
          showSizeChanger: false,
          hideOnSinglePage: filtered.length <= 12,
          onChange: setPage,
        }}
        scroll={{ x: 760 }}
        rowSelection={{ selectedRowKeys, onChange: onSelectionChange }}
        loading={nodes.length === 0}
        locale={{ emptyText: nodes.length > 0 ? t('pages.vpngate.filterEmpty') : emptyText }}
        rowClassName={(node) => (node.active ? 'vg-row-active' : '')}
      />
    </>
  );
}
