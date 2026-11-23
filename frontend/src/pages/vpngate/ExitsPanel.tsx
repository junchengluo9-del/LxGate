import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip,
  Segmented,
} from 'antd';
import {
  ApiOutlined,
  DeleteOutlined,
  DisconnectOutlined,
  EditOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import {
  useConnectVpngateExit,
  useCreateVpngateExit,
  useDeleteVpngateExit,
  useDisconnectVpngateExit,
  useUpdateVpngateExit,
  type VpngateExit,
  type VpngateExitsPayload,
} from '@/api/queries/useVpngateExits';

type ToastKind = 'success' | 'info' | 'warning' | 'error';

interface ExitEditDraft {
  id: string;
  name: string;
  mode: 'auto' | 'fixed_region' | 'fixed_ip' | 'favorites';
  country: string;
  ipType: string;
  favFallback: boolean;
}

const MODES = ['auto', 'fixed_region', 'fixed_ip', 'favorites'] as const;
const IP_TYPES = ['all', 'residential', 'hosting'] as const;

const IP_TYPE_COLORS: Record<string, string> = {
  residential: 'green',
  mobile: 'geekblue',
  hosting: 'orange',
};

function exitStatus(exit: VpngateExit): 'on' | 'busy' | 'off' {
  if (exit.connecting) return 'busy';
  return exit.running ? 'on' : 'off';
}

function nodeLabel(exit: VpngateExit, fallback: string) {
  if (!exit.nodeId) return fallback;
  const node = exit.node;
  if (!node) return exit.nodeId;
  const country = node.country || node.country_short || fallback;
  const ip = node.ip || node.host_name || '';
  return ip ? `${country} · ${ip}` : country;
}

export default function ExitsPanel({
  data,
  isMobile,
  countries,
  onToast,
  onError,
}: {
  data: VpngateExitsPayload | undefined;
  isMobile: boolean;
  countries: { value: string; label: string }[];
  onToast: (kind: ToastKind, text: string) => void;
  onError: (error: Error) => void;
}) {
  const { t } = useTranslation();
  const createExit = useCreateVpngateExit();
  const updateExit = useUpdateVpngateExit();
  const deleteExit = useDeleteVpngateExit();
  const connectExit = useConnectVpngateExit();
  const disconnectExit = useDisconnectVpngateExit();

  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [draft, setDraft] = useState<ExitEditDraft | null>(null);

  const exits = data?.exits ?? [];
  const memory = data?.memory;
  const busy = createExit.isPending || updateExit.isPending || deleteExit.isPending;

  const memoryText = useMemo(() => {
    if (!memory) return '';
    return t('pages.vpngate.exits.memoryLine', {
      active: memory.activeExits,
      configured: memory.configuredExits,
      available: memory.availableMb,
      reserve: memory.reserveMb,
      perTunnel: memory.perTunnelMb,
    });
  }, [memory, t]);

  const memoryPercent = useMemo(() => {
    if (!memory || !memory.totalMb) return 0;
    const usable = Math.max(1, memory.totalMb - memory.reserveMb);
    const used = Math.max(0, memory.totalMb - memory.availableMb);
    return Math.min(100, Math.round((used / usable) * 100));
  }, [memory]);

  const openEditor = (exit: VpngateExit) => {
    setDraft({
      id: exit.id,
      name: exit.name,
      mode: (exit.routing?.routing_mode ?? 'auto') as ExitEditDraft['mode'],
      country: (exit.routing?.force_country ?? '').toUpperCase(),
      ipType: exit.routing?.routing_ip_type ?? 'all',
      favFallback: exit.routing?.fav_fail_fallback !== false,
    });
  };

  const submitEditor = () => {
    if (!draft) return;
    updateExit.mutate(
      {
        id: draft.id,
        name: draft.name.trim() || undefined,
        routing_mode: draft.mode,
        force_country: draft.country,
        routing_ip_type: draft.ipType,
        fav_fail_fallback: draft.favFallback,
      },
      {
        onSuccess: (msg) => {
          onToast('success', (msg as { msg?: string })?.msg || t('pages.vpngate.exits.saved'));
          setDraft(null);
        },
        onError,
      },
    );
  };

  const submitCreate = () => {
    createExit.mutate(newName.trim(), {
      onSuccess: (msg) => {
        onToast('success', (msg as { msg?: string })?.msg || t('pages.vpngate.exits.created'));
        setNewName('');
        setCreating(false);
      },
      onError,
    });
  };

  const confirmDelete = (exit: VpngateExit) => {
    Modal.confirm({
      title: t('pages.vpngate.exits.deleteTitle', { name: exit.name }),
      content: t('pages.vpngate.exits.deleteBody'),
      okText: t('pages.vpngate.exits.delete'),
      okButtonProps: { danger: true },
      cancelText: t('cancel'),
      onOk: () =>
        new Promise<void>((resolve) => {
          deleteExit.mutate(exit.id, {
            onSuccess: () => {
              onToast('success', t('pages.vpngate.exits.deleted', { name: exit.name }));
              resolve();
            },
            onError: (error: Error) => {
              onError(error);
              resolve();
            },
          });
        }),
    });
  };

  return (
    <div className="lg-card vg-exits">
      <div className="vg-exits-head">
        <div>
          <h3 className="lg-title">{t('pages.vpngate.exits.title')}</h3>
          <p className="lg-subtitle">{t('pages.vpngate.exits.hint')}</p>
        </div>
        <Space wrap>
          <Button icon={<ApiOutlined />} onClick={() => setCreating(true)} disabled={busy}>
            {t('pages.vpngate.exits.create')}
          </Button>
        </Space>
      </div>

      {memory && (
        <div className={`vg-memory${memory.canStartNew ? '' : ' is-low'}`}>
          <div className="vg-memory-track" role="img" aria-label={memoryText}>
            <span className="vg-memory-fill" style={{ width: `${memoryPercent}%` }} />
          </div>
          <div className="vg-memory-meta">
            <span className="lg-subtitle">{memoryText}</span>
            <span className={`lg-pill ${memory.canStartNew ? 'ok' : 'warn'}`}>
              <span className="lg-dot" aria-hidden />
              {memory.canStartNew
                ? t('pages.vpngate.exits.memoryOk', { count: memory.maxActiveExits })
                : t('pages.vpngate.exits.memoryLow')}
            </span>
          </div>
        </div>
      )}

      {!memory?.canStartNew && (
        <Alert
          type="warning"
          showIcon
          message={t('pages.vpngate.exits.memoryWarnTitle')}
          description={t('pages.vpngate.exits.memoryWarnBody')}
        />
      )}
      {data?.engineError && <Alert type="warning" showIcon message={data.engineError} />}

      {exits.length === 0 ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            data?.engineError
              ? t('pages.vpngate.exits.unavailable')
              : t('pages.vpngate.exits.empty')
          }
        />
      ) : (
        <div className="vg-exit-grid">
          {exits.map((exit) => {
            const status = exitStatus(exit);
            const node = exit.node;
            const ipType = (node?.ip_type || '').toLowerCase();
            return (
              <article key={exit.id} className={`vg-exit-card ${status}`}>
                <header className="vg-exit-head">
                  <div className="vg-exit-identity">
                    <span className="vg-exit-name">{exit.name}</span>
                    <Tag bordered={false} className="vg-exit-slot">
                      {t('pages.vpngate.exits.slot', { slot: exit.slot })}
                    </Tag>
                  </div>
                  <span
                    className={`lg-pill ${status === 'on' ? 'ok' : status === 'busy' ? 'warn' : 'bad'}`}
                  >
                    <span className="lg-dot" aria-hidden />
                    {status === 'on'
                      ? t('pages.vpngate.connected')
                      : status === 'busy'
                        ? t('pages.vpngate.connecting')
                        : t('pages.vpngate.disconnected')}
                  </span>
                </header>

                <div className="vg-exit-node">
                  <span className="lg-mono">
                    {nodeLabel(exit, t('pages.vpngate.exits.noNode'))}
                  </span>
                  <Space size={6} wrap>
                    {ipType && IP_TYPE_COLORS[ipType] && (
                      <Tag bordered={false} color={IP_TYPE_COLORS[ipType]}>
                        {t(`pages.vpngate.ipTypeTags.${ipType}`)}
                      </Tag>
                    )}
                    {exit.latencyMs || node?.latency_ms ? (
                      <span className="vg-latency ok">
                        {Math.round(exit.latencyMs || node?.latency_ms || 0)} ms
                      </span>
                    ) : null}
                  </Space>
                </div>

                <p className="vg-exit-stage lg-subtitle">
                  {exit.stageDetail || exit.stage || t('pages.vpngate.exits.idle')}
                </p>
                {exit.lastError && status === 'off' && (
                  <p className="vg-exit-error lg-subtitle">{exit.lastError}</p>
                )}

                <div className="vg-exit-tech lg-mono">
                  <span>{exit.dev}</span>
                  <span>table {exit.table}</span>
                  <span>:{exit.proxyPort}</span>
                  <span>{exit.outboundTag}</span>
                </div>

                <footer className="vg-exit-actions">
                  <Space size={6} wrap>
                    <Tooltip title={t('pages.vpngate.exits.connectHint')}>
                      <Button
                        size="small"
                        type="primary"
                        icon={<ThunderboltOutlined />}
                        loading={connectExit.isPending}
                        onClick={() =>
                          connectExit.mutate(
                            { exitId: exit.id },
                            {
                              onSuccess: (msg) =>
                                onToast(
                                  'success',
                                  (msg as { msg?: string })?.msg ||
                                    t('pages.vpngate.exits.connectedToast'),
                                ),
                              onError,
                            },
                          )
                        }
                      >
                        {exit.running
                          ? t('pages.vpngate.exits.switchNode')
                          : t('pages.vpngate.connect')}
                      </Button>
                    </Tooltip>
                    <Button
                      size="small"
                      icon={<EditOutlined />}
                      onClick={() => openEditor(exit)}
                      disabled={busy}
                    >
                      {t('pages.vpngate.exits.policy')}
                    </Button>
                    <Button
                      size="small"
                      danger
                      icon={<DisconnectOutlined />}
                      disabled={!exit.running || disconnectExit.isPending}
                      onClick={() =>
                        disconnectExit.mutate(exit.id, {
                          onSuccess: () =>
                            onToast('success', t('pages.vpngate.toasts.disconnected')),
                          onError,
                        })
                      }
                    >
                      {t('pages.vpngate.disconnect')}
                    </Button>
                    {!exit.isDefault && (
                      <Button
                        size="small"
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() => confirmDelete(exit)}
                        disabled={busy}
                      >
                        {t('pages.vpngate.exits.delete')}
                      </Button>
                    )}
                  </Space>
                </footer>
              </article>
            );
          })}
        </div>
      )}

      <Modal
        title={t('pages.vpngate.exits.createTitle')}
        open={creating}
        onCancel={() => setCreating(false)}
        onOk={submitCreate}
        okText={t('pages.vpngate.exits.create')}
        cancelText={t('cancel')}
        confirmLoading={createExit.isPending}
        destroyOnHidden
      >
        <p className="lg-subtitle">{t('pages.vpngate.exits.createHint')}</p>
        <Input
          value={newName}
          maxLength={48}
          placeholder={t('pages.vpngate.exits.namePlaceholder', {
            slot: (memory?.configuredExits ?? exits.length) + 1,
          })}
          onChange={(event) => setNewName(event.target.value)}
          onPressEnter={submitCreate}
        />
      </Modal>

      <Modal
        title={draft ? t('pages.vpngate.exits.editTitle', { name: draft.name }) : ''}
        open={!!draft}
        onCancel={() => setDraft(null)}
        onOk={submitEditor}
        okText={t('pages.vpngate.exits.save')}
        cancelText={t('cancel')}
        confirmLoading={updateExit.isPending}
        width={isMobile ? undefined : 560}
        destroyOnHidden
      >
        {draft && (
          <div className="vg-exit-editor">
            <label className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.exits.name')}</span>
              <Input
                value={draft.name}
                maxLength={48}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              />
            </label>

            <div className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.mode')}</span>
              <Segmented
                block={isMobile}
                value={draft.mode}
                onChange={(value) => setDraft({ ...draft, mode: value as ExitEditDraft['mode'] })}
                options={MODES.map((value) => ({
                  value,
                  label: t(`pages.vpngate.modes.${value}`),
                }))}
              />
            </div>

            <label className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.country')}</span>
              <Select
                showSearch
                allowClear
                disabled={draft.mode !== 'fixed_region'}
                value={draft.country || undefined}
                placeholder={t('pages.vpngate.countryPlaceholder')}
                options={countries}
                onChange={(value) => setDraft({ ...draft, country: (value ?? '').toUpperCase() })}
                style={{ minWidth: 180 }}
              />
            </label>

            <label className="vg-field">
              <span className="vg-field-label">{t('pages.vpngate.ipType')}</span>
              <Select
                value={draft.ipType}
                onChange={(value) => setDraft({ ...draft, ipType: value })}
                options={IP_TYPES.map((value) => ({
                  value,
                  label: t(`pages.vpngate.ipTypes.${value}`),
                }))}
                style={{ minWidth: 150 }}
              />
            </label>

            <label className="vg-field vg-field-switch">
              <span className="vg-field-label">{t('pages.vpngate.favFallback')}</span>
              <Switch
                checked={draft.favFallback}
                onChange={(checked) => setDraft({ ...draft, favFallback: checked })}
              />
            </label>

            <p className="lg-subtitle">{t('pages.vpngate.exits.policyHint')}</p>
          </div>
        )}
      </Modal>
    </div>
  );
}

/** Clickable exit choice for the shared node table. */
export function ExitConnectMenu({
  exits,
  nodeId,
  occupiedBy,
  loading,
  onPick,
}: {
  exits: VpngateExit[];
  nodeId: string;
  occupiedBy?: { exitId?: string; name?: string };
  loading: boolean;
  onPick: (exitId: string) => void;
}) {
  const { t } = useTranslation();
  if (exits.length === 0) {
    return (
      <Button size="small" disabled>
        {t('pages.vpngate.connect')}
      </Button>
    );
  }
  if (exits.length === 1) {
    const only = exits[0];
    const taken = occupiedBy?.exitId && occupiedBy.exitId !== only.id;
    return (
      <Tooltip
        title={
          taken ? t('pages.vpngate.exits.occupiedBy', { name: occupiedBy?.name ?? '' }) : undefined
        }
      >
        <Button
          size="small"
          type="primary"
          icon={<ThunderboltOutlined />}
          loading={loading}
          disabled={!!taken}
          onClick={() => onPick(only.id)}
        >
          {only.name}
        </Button>
      </Tooltip>
    );
  }
  return (
    <Select
      size="small"
      value={undefined}
      placeholder={t('pages.vpngate.exits.pickExit')}
      style={{ minWidth: 132 }}
      loading={loading}
      onSelect={(exitId) => onPick(String(exitId))}
      optionLabelProp="label"
      options={exits.map((exit) => {
        const taken = occupiedBy?.exitId && occupiedBy.exitId !== exit.id;
        return {
          value: exit.id,
          label: exit.name,
          disabled: !!taken,
          title: taken
            ? t('pages.vpngate.exits.occupiedBy', { name: occupiedBy?.name ?? '' })
            : undefined,
        };
      })}
      data-node={nodeId}
    />
  );
}
