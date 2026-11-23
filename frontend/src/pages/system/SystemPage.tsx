import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Descriptions,
  Input,
  Layout,
  Progress,
  Result,
  Segmented,
  Space,
  Tag,
  Tooltip,
  message,
} from 'antd';
import {
  CheckCircleOutlined,
  CloudDownloadOutlined,
  CodeOutlined,
  CopyOutlined,
  GithubOutlined,
  KeyOutlined,
  ReloadOutlined,
  SaveOutlined,
} from '@ant-design/icons';

import { ClipboardManager, HttpUtil } from '@/utils';
import { useTheme } from '@/hooks/useTheme';
import { formatPanelVersion } from '@/lib/panel-version';
import AppSidebar from '@/layouts/AppSidebar';
import TopStatusBar from '@/layouts/TopStatusBar';
import { setMessageInstance } from '@/utils/messageBus';
import './SystemPage.css';

interface VersionPayload {
  currentVersion?: string;
  channel?: string;
  latestVersion?: string;
  updateAvailable?: boolean;
  changelog?: string;
  releaseName?: string;
  releaseUrl?: string;
  publishedAt?: string;
  checkedAt?: string;
  repo?: string;
  isDocker?: boolean;
  canSelfUpdate?: boolean;
  updateCommand?: string;
  checkError?: string;
  prerelease?: boolean;
  latestCommit?: string;
  currentCommit?: string;
}

interface InfoPayload {
  name?: string;
  version?: string;
  buildCommit?: string;
  buildDate?: string;
  devBuild?: boolean;
  isDocker?: boolean;
  repo?: string;
  dbPath?: string;
  vpngateDataDir?: string;
  updateCommand?: string;
}

interface UpdateResult {
  started?: boolean;
  mode?: string;
  runId?: string;
  command?: string;
  message?: string;
  preserveData?: string[];
}

interface UpdateSettingsPayload {
  channel?: string;
  hasGithubToken?: boolean;
  githubTokenMasked?: string;
  tokenFileInPlace?: boolean;
}

interface UpdateStatus {
  state?: string;
  runId?: string;
  message?: string;
}

const VERSION_POLL_MS = 15 * 60_000;

export default function SystemPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const [updateRunId, setUpdateRunId] = useState('');

  const versionQuery = useQuery({
    queryKey: ['system', 'version'],
    queryFn: async (): Promise<VersionPayload> => {
      const msg = await HttpUtil.get<VersionPayload>('/panel/api/system/version', undefined, {
        silent: true,
      });
      if (!msg?.success) throw new Error(msg?.msg || 'version check failed');
      return msg.obj ?? {};
    },
    refetchInterval: VERSION_POLL_MS,
    refetchIntervalInBackground: false,
    retry: false,
  });

  const infoQuery = useQuery({
    queryKey: ['system', 'info'],
    queryFn: async (): Promise<InfoPayload> => {
      const msg = await HttpUtil.get<InfoPayload>('/panel/api/system/info', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    staleTime: Infinity,
    retry: false,
  });

  const settingsQuery = useQuery({
    queryKey: ['system', 'update-settings'],
    queryFn: async (): Promise<UpdateSettingsPayload> => {
      const msg = await HttpUtil.get<UpdateSettingsPayload>(
        '/panel/api/system/update-settings',
        undefined,
        {
          silent: true,
        },
      );
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    retry: false,
  });

  const statusQuery = useQuery({
    queryKey: ['system', 'update-status', updateRunId],
    queryFn: async (): Promise<UpdateStatus> => {
      const msg = await HttpUtil.get<UpdateStatus>('/panel/api/system/update-status', undefined, {
        silent: true,
      });
      return msg?.success ? (msg.obj ?? {}) : {};
    },
    enabled: !!updateRunId,
    refetchInterval: 5000,
    retry: false,
  });

  const [channelDraft, setChannelDraft] = useState('stable');
  const [tokenDraft, setTokenDraft] = useState('');
  const savedChannel = settingsQuery.data?.channel || 'stable';
  const hasToken = !!settingsQuery.data?.hasGithubToken;

  useEffect(() => {
    if (settingsQuery.data?.channel) setChannelDraft(settingsQuery.data.channel);
  }, [settingsQuery.data?.channel]);

  // Saving a channel or a token changes which release the panel sees, so both
  // paths persist first and then re-run the version check the page displays.
  const settingsMutation = useMutation({
    mutationFn: async (body: {
      channel?: string;
      githubToken?: string;
      clearGithubToken?: boolean;
    }): Promise<UpdateSettingsPayload> => {
      const msg = await HttpUtil.post<UpdateSettingsPayload>(
        '/panel/api/system/update-settings',
        body,
        { silent: true },
      );
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg.obj ?? {};
    },
    onSuccess: async (payload, body) => {
      setTokenDraft('');
      await settingsQuery.refetch();
      await versionQuery.refetch();
      if (body.clearGithubToken) messageApi.success(t('pages.system.tokenCleared'));
      else if (body.githubToken) messageApi.success(t('pages.system.tokenSaved'));
      else messageApi.success(t('pages.system.channelSaved'));
      if (payload.channel) setChannelDraft(payload.channel);
    },
    onError: (error: Error) => messageApi.error(error.message),
  });

  const updateMutation = useMutation({
    mutationFn: async (): Promise<UpdateResult> => {
      const msg = await HttpUtil.post<UpdateResult>(
        '/panel/api/system/update',
        {},
        { silent: true },
      );
      if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
      return msg.obj ?? {};
    },
    onSuccess: (result) => {
      if (result.started) {
        setUpdateRunId(result.runId ?? '');
        messageApi.success(result.message || t('pages.system.toasts.updateStarted'));
      } else {
        messageApi.info(result.command || t('pages.system.dockerHint'));
      }
    },
    onError: (error: Error) => messageApi.error(error.message),
  });

  const version = versionQuery.data;
  const info = infoQuery.data;
  const updateState = statusQuery.data?.state ?? '';

  const copyCommand = useCallback(async (command: string) => {
    await ClipboardManager.copyText(command);
  }, []);

  const percent = progressForState(updateState);

  const pageClass = `system-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();
  const docker = version?.isDocker ?? info?.isDocker ?? false;
  const command = version?.updateCommand || info?.updateCommand || '';

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <TopStatusBar />
          <Layout.Content className="content-area">
            {versionQuery.isError ? (
              <Result
                status="error"
                title={t('somethingWentWrong')}
                subTitle={(versionQuery.error as Error)?.message}
                extra={
                  <Button
                    type="primary"
                    icon={<ReloadOutlined />}
                    onClick={() => versionQuery.refetch()}
                  >
                    {t('refresh')}
                  </Button>
                }
              />
            ) : (
              <div className="sys-page lg-stack">
                <Card className="sys-hero">
                  <div className="sys-hero-main">
                    <div>
                      <p className="lg-subtitle">{info?.name || 'LxGate'}</p>
                      <h2 className="sys-version lg-num">
                        {formatPanelVersion(version?.currentVersion) || '—'}
                      </h2>
                      <Space wrap size={8}>
                        {version?.checkError ? (
                          /* A failed release check is not "up to date": the private
                             repo answers 404 without a token, and the green badge
                             used to report that as the best possible state. */
                          <Tooltip title={t('pages.system.tokenRequiredHint')}>
                            <Tag bordered={false}>{t('pages.system.notChecked')}</Tag>
                          </Tooltip>
                        ) : (
                          <Tag bordered={false} color={version?.updateAvailable ? 'gold' : 'green'}>
                            {version?.updateAvailable
                              ? t('pages.system.updateAvailable')
                              : t('pages.system.upToDate')}
                          </Tag>
                        )}
                        {docker && <Tag bordered={false}>Docker</Tag>}
                        {info?.devBuild && <Tag bordered={false}>dev</Tag>}
                        {version?.channel && (
                          <Tag
                            bordered={false}
                            color={version.channel === 'dev' ? 'purple' : 'blue'}
                          >
                            {channelLabel(t, version.channel)}
                          </Tag>
                        )}
                        {version?.prerelease && <Tag bordered={false}>dev-latest</Tag>}
                        {version?.latestVersion && (
                          <span className="lg-muted lg-num">
                            {t('pages.system.latest', { version: version.latestVersion })}
                          </span>
                        )}
                      </Space>
                    </div>
                    <Space wrap>
                      <Button
                        icon={<ReloadOutlined />}
                        loading={versionQuery.isFetching}
                        onClick={() => versionQuery.refetch()}
                      >
                        {t('pages.system.checkAgain')}
                      </Button>
                      <Button
                        type="primary"
                        icon={<CloudDownloadOutlined />}
                        disabled={!docker && !version?.updateAvailable}
                        loading={updateMutation.isPending}
                        onClick={() => updateMutation.mutate()}
                      >
                        {docker ? t('pages.system.showCommand') : t('pages.system.updateNow')}
                      </Button>
                    </Space>
                  </div>

                  {version?.checkError && (
                    <Alert
                      className="sys-alert"
                      type="info"
                      showIcon
                      message={t('pages.system.checkFailed')}
                      description={version.checkError}
                    />
                  )}

                  {updateMutation.data &&
                    !updateMutation.data.started &&
                    updateMutation.data.command && (
                      <div className="sys-command">
                        <p className="lg-subtitle">{t('pages.system.dockerHint')}</p>
                        <div className="sys-command-row">
                          <Input readOnly value={updateMutation.data.command} className="lg-mono" />
                          <Button
                            icon={<CopyOutlined />}
                            onClick={() => copyCommand(updateMutation.data?.command ?? '')}
                          >
                            {t('copy')}
                          </Button>
                        </div>
                        {updateMutation.data.preserveData && (
                          <p className="lg-subtitle lg-mono">
                            {updateMutation.data.preserveData.join(' · ')}
                          </p>
                        )}
                      </div>
                    )}

                  {updateRunId && (
                    <div className="sys-command">
                      <p className="lg-subtitle">
                        {t('pages.system.updateRunning')} — {updateState || 'pending'}
                      </p>
                      <Progress percent={percent} status="active" showInfo={false} />
                      {statusQuery.data?.message && (
                        <p className="lg-subtitle">{statusQuery.data.message}</p>
                      )}
                    </div>
                  )}
                </Card>

                <Card title={t('pages.system.updateSettingsTitle')} className="sys-settings">
                  <p className="lg-subtitle sys-settings-hint">
                    {t('pages.system.updateSettingsHint')}
                  </p>

                  <div className="sys-setting-row">
                    <div className="sys-setting-copy">
                      <p className="lg-title">{t('pages.system.channelLabel')}</p>
                      <p className="lg-subtitle">{t('pages.system.channelHint')}</p>
                    </div>
                    <Segmented
                      className="sys-channel"
                      value={channelDraft}
                      disabled={settingsMutation.isPending}
                      options={[
                        { label: t('pages.system.channelStable'), value: 'stable' },
                        { label: t('pages.system.channelDev'), value: 'dev' },
                      ]}
                      onChange={(value) => {
                        const next = String(value);
                        setChannelDraft(next);
                        if (next !== savedChannel) settingsMutation.mutate({ channel: next });
                      }}
                    />
                  </div>

                  <div className="sys-setting-row">
                    <div className="sys-setting-copy">
                      <p className="lg-title">
                        <KeyOutlined aria-hidden /> {t('pages.system.tokenLabel')}
                      </p>
                      <p className="lg-subtitle">{t('pages.system.tokenHint')}</p>
                    </div>
                    <Tag
                      bordered={false}
                      color={hasToken ? 'green' : 'default'}
                      className="sys-token-state"
                    >
                      {hasToken
                        ? t('pages.system.tokenConfigured', {
                            mask: settingsQuery.data?.githubTokenMasked || '••••',
                          })
                        : t('pages.system.tokenNotConfigured')}
                    </Tag>
                  </div>

                  <div className="sys-token-row">
                    <Input.Password
                      value={tokenDraft}
                      onChange={(event) => setTokenDraft(event.target.value)}
                      placeholder={t('pages.system.tokenPlaceholder')}
                      autoComplete="off"
                      spellCheck={false}
                      className="lg-mono"
                      aria-label={t('pages.system.tokenLabel')}
                    />
                    <Button
                      type="primary"
                      icon={<SaveOutlined />}
                      disabled={!tokenDraft.trim()}
                      loading={settingsMutation.isPending}
                      onClick={() => settingsMutation.mutate({ githubToken: tokenDraft.trim() })}
                    >
                      {t('pages.system.tokenSave')}
                    </Button>
                    {hasToken && (
                      <Button
                        danger
                        disabled={settingsMutation.isPending}
                        onClick={() => settingsMutation.mutate({ clearGithubToken: true })}
                      >
                        {t('pages.system.tokenClear')}
                      </Button>
                    )}
                  </div>
                  <p className="lg-subtitle sys-token-hint">
                    {t('pages.system.tokenRequiredHint')}
                  </p>
                </Card>

                <Card title={t('pages.system.changelogTitle')} className="sys-changelog">
                  {version?.changelog ? (
                    <>
                      <Space wrap size={8} className="sys-changelog-head">
                        {version.releaseName && <strong>{version.releaseName}</strong>}
                        {version.publishedAt && (
                          <span className="lg-muted lg-num">
                            {new Date(version.publishedAt).toLocaleString()}
                          </span>
                        )}
                        {version.releaseUrl && (
                          <a href={version.releaseUrl} target="_blank" rel="noopener noreferrer">
                            <GithubOutlined /> {t('pages.system.viewRelease')}
                          </a>
                        )}
                      </Space>
                      <pre className="lg-log sys-changelog-body">{version.changelog}</pre>
                    </>
                  ) : (
                    <p className="lg-muted">{t('pages.system.noChangelog')}</p>
                  )}
                </Card>

                <Card title={t('pages.system.infoTitle')} className="sys-info">
                  <Descriptions column={{ xs: 1, sm: 1, md: 2 }} size="small" bordered={false}>
                    <Descriptions.Item label={t('pages.system.fields.version')}>
                      <span className="lg-num">
                        {info?.version || version?.currentVersion || '—'}
                      </span>
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.repo')}>
                      {info?.repo ? (
                        <a
                          href={`https://github.com/${info.repo}`}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          <GithubOutlined /> {info.repo}
                        </a>
                      ) : (
                        '—'
                      )}
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.commit')}>
                      <span className="lg-mono">
                        {info?.buildCommit || t('pages.system.fields.none')}
                      </span>
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.buildDate')}>
                      <span className="lg-mono">
                        {info?.buildDate || t('pages.system.fields.none')}
                      </span>
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.dbPath')}>
                      <span className="lg-mono">{info?.dbPath || '—'}</span>
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.vpngateData')}>
                      <span className="lg-mono">{info?.vpngateDataDir || '—'}</span>
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.mode')}>
                      {docker ? 'Docker' : t('pages.system.fields.native')}
                    </Descriptions.Item>
                    <Descriptions.Item label={t('pages.system.fields.updateCommand')}>
                      <span className="lg-mono">{command || '—'}</span>
                    </Descriptions.Item>
                  </Descriptions>

                  <div className="sys-preserve">
                    <CodeOutlined aria-hidden />
                    <span>{t('pages.system.preserveHint')}</span>
                  </div>
                  {version?.checkedAt && (
                    <p className="lg-subtitle lg-num">
                      {t('pages.system.checkedAt', {
                        time: new Date(version.checkedAt).toLocaleString(),
                      })}
                    </p>
                  )}
                  {!version?.updateAvailable && !version?.checkError && (
                    <p className="lg-subtitle">
                      <CheckCircleOutlined /> {t('pages.system.noUpdateHint')}
                    </p>
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

function channelLabel(t: (key: string) => string, channel: string): string {
  return channel === 'dev' ? t('pages.system.channelDev') : t('pages.system.channelStable');
}

function progressForState(state: string): number {
  switch (state) {
    case 'success':
      return 100;
    case 'failed':
      return 100;
    case 'pending':
      return 45;
    default:
      return 20;
  }
}
