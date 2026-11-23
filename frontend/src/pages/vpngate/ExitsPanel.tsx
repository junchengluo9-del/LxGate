import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { Alert, Button, Empty, Input, Modal, Space, Tag, Tooltip } from 'antd';
import {
  ApiOutlined,
  CheckCircleFilled,
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
  type VpngateExit,
  type VpngateExitsPayload,
} from '@/api/queries/useVpngateExits';

type ToastKind = 'success' | 'info' | 'warning' | 'error';

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

/**
 * The engine names a fresh slot 「出口 N」, which repeats the slot badge right
 * next to it. A slot the operator renamed keeps its badge — that badge is then
 * the only place the slot number appears.
 */
function keepsSlotBadge(exit: VpngateExit) {
  const match = /^(?:出口|exit)\s*(\d+)$/i.exec(exit.name.trim());
  return !match || Number(match[1]) !== exit.slot;
}

/**
 * One card per residential exit.
 *
 * The cards are a master control: clicking one selects it, and everything below
 * the panel (inbound binding, status tiles, routing policy, the node table's
 * connect target) follows the selection. Action buttons stop the click so
 * pressing "disconnect" on a card never silently changes what is selected.
 */
export default function ExitsPanel({
  data,
  selectedId,
  onSelect,
  onOpenPolicy,
  onToast,
  onError,
}: {
  data: VpngateExitsPayload | undefined;
  selectedId: string;
  onSelect: (id: string) => void;
  /** Selects the exit and walks the operator down to its routing policy. */
  onOpenPolicy: (id: string) => void;
  onToast: (kind: ToastKind, text: string) => void;
  onError: (error: Error) => void;
}) {
  const { t } = useTranslation();
  const createExit = useCreateVpngateExit();
  const deleteExit = useDeleteVpngateExit();
  const connectExit = useConnectVpngateExit();
  const disconnectExit = useDisconnectVpngateExit();

  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');

  const exits = data?.exits ?? [];
  const memory = data?.memory;
  const bindings = data?.bindings ?? {};
  const busy = createExit.isPending || deleteExit.isPending;
  const selected = exits.find((exit) => exit.id === selectedId) ?? exits[0];

  const boundCount = useMemo(() => {
    const counts = new Map<number, number>();
    for (const slot of Object.values(bindings)) {
      counts.set(slot, (counts.get(slot) ?? 0) + 1);
    }
    return counts;
  }, [bindings]);

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

      {/* The selection used to be announced inside the selected card, which pushed
          that card's text and buttons a line below its neighbours'. It belongs to
          the whole grid, because it is true of every panel underneath it. */}
      {selected && exits.length > 0 && (
        <p className="vg-exit-selected lg-subtitle">
          <CheckCircleFilled aria-hidden />
          {t('pages.vpngate.exits.selectedHint', { name: selected.name })}
        </p>
      )}

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
            const isSelected = exit.id === selectedId;
            const bound = boundCount.get(exit.slot) ?? 0;
            const stageLine =
              status === 'on'
                ? t('pages.vpngate.exits.connectedSince', {
                    time: exit.connectedAt ? dayjs.unix(exit.connectedAt).format('HH:mm') : '—',
                  })
                : status === 'busy'
                  ? t(`pages.vpngate.stages.${exit.stage || 'idle'}`, {
                      defaultValue: t('pages.vpngate.connecting'),
                    })
                  : (exit.lastError ?? '');
            return (
              <article
                key={exit.id}
                className={`vg-exit-card ${status}${isSelected ? ' is-selected' : ''}`}
                onClick={() => onSelect(exit.id)}
              >
                <header className="vg-exit-head">
                  <div className="vg-exit-identity">
                    {/* The named control is the keyboard path to selection; the
                        card's own click is the mouse shortcut for the same act. */}
                    <button
                      type="button"
                      className="vg-exit-pick"
                      aria-pressed={isSelected}
                      aria-label={t('pages.vpngate.exits.select', { name: exit.name })}
                      onClick={(event) => {
                        event.stopPropagation();
                        onSelect(exit.id);
                      }}
                    >
                      {isSelected ? (
                        <CheckCircleFilled className="vg-exit-pick-icon" aria-hidden />
                      ) : (
                        <span className="vg-exit-pick-dot" aria-hidden />
                      )}
                      <span className="vg-exit-name">{exit.name}</span>
                    </button>
                    {keepsSlotBadge(exit) && (
                      <Tag bordered={false} className="vg-exit-slot">
                        {t('pages.vpngate.exits.slot', { slot: exit.slot })}
                      </Tag>
                    )}
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

                {/* One status line, one sentence: connected says since when, a
                    connecting exit says which step it is on (the engine emits a
                    code, the panel owns the wording), an offline exit stays
                    quiet unless the last attempt failed. The line is always
                    rendered so every card's rows stay level. */}
                <p
                  className={`vg-exit-stage lg-subtitle${status === 'off' && exit.lastError ? ' is-error' : ''}`}
                >
                  {stageLine}
                </p>

                <div className="vg-exit-tech lg-mono">
                  <span>{exit.dev}</span>
                  <span>table {exit.table}</span>
                  <span>:{exit.proxyPort}</span>
                  <span>{exit.outboundTag}</span>
                  <span>
                    {t('pages.vpngate.exits.boundCount', {
                      count: bound,
                      total: data?.inbounds?.length ?? 0,
                    })}
                  </span>
                </div>

                <footer className="vg-exit-actions" onClick={(event) => event.stopPropagation()}>
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
                      onClick={() => onOpenPolicy(exit.id)}
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
    </div>
  );
}
