import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ComponentType } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Drawer, Layout, Menu, Tooltip } from 'antd';
import type { MenuProps } from 'antd';
import {
  ApartmentOutlined,
  CloseOutlined,
  CloudServerOutlined,
  ClusterOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DiscordOutlined,
  GithubOutlined,
  LogoutOutlined,
  MailOutlined,
  MenuFoldOutlined,
  MenuOutlined,
  MoonFilled,
  MoonOutlined,
  ReadOutlined,
  SafetyOutlined,
  SearchOutlined,
  SettingOutlined,
  SunOutlined,
  TelegramFilled,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { formatPanelVersion } from '@/lib/panel-version';
import { pauseAnimationsUntilLeave, useTheme } from '@/hooks/useTheme';
import { useAllSettings } from '@/api/queries/useAllSettings';
import { useCommandPalette } from '@/components/command-palette/useCommandPalette';
import { BRAND_NAME, REPO_URL } from '@/lib/brand';
import { PANEL_ROUTES } from '@/routes/panelRoutes';
import './AppSidebar.css';

// The palette listens for Ctrl as well as Cmd, so the chip must not show a
// Mac glyph to the Linux and Windows operators who are most of this panel's.
const SHORTCUT_MODIFIER = /Mac|iPhone|iPad|iPod/.test(navigator.userAgent) ? '⌘' : 'Ctrl';
const DOCS_URL = 'https://docs.sanaei.dev/';
const LOGOUT_KEY = '__logout__';
// Desktop rail widths. The expanded sidebar is wide enough for the full
// wordmark and every menu label; the rail is icon-only.
const RAIL_WIDTH = 76;
const SIDER_WIDTH = 232;
const SIDEBAR_COLLAPSED_KEY = 'sidebar-collapsed';

function DocsButton({ ariaLabel }: { ariaLabel: string }) {
  return (
    <a
      href={DOCS_URL}
      target="_blank"
      rel="noopener noreferrer"
      className="sidebar-docs"
      aria-label={ariaLabel}
      title={ariaLabel}
    >
      <ReadOutlined aria-hidden />
    </a>
  );
}

function VersionBadge({ version, collapsed }: { version: string; collapsed?: boolean }) {
  if (!version) return null;
  const label = formatPanelVersion(version);
  return (
    <a
      href={REPO_URL}
      target="_blank"
      rel="noopener noreferrer"
      className="sider-version"
      aria-label={`GitHub ${label}`}
      title={label}
    >
      <GithubOutlined aria-hidden />
      {!collapsed && <span className="sider-version-text">{label}</span>}
    </a>
  );
}

function ThemeCycleButton({
  id,
  isDark,
  isUltra,
  onCycle,
  ariaLabel,
}: {
  id: string;
  isDark: boolean;
  isUltra: boolean;
  onCycle: () => void;
  ariaLabel: string;
}) {
  const icon = !isDark ? (
    <SunOutlined aria-hidden />
  ) : !isUltra ? (
    <MoonOutlined aria-hidden />
  ) : (
    <MoonFilled aria-hidden />
  );
  return (
    <button
      id={id}
      type="button"
      className="sidebar-theme-cycle"
      aria-label={ariaLabel}
      title={ariaLabel}
      onClick={onCycle}
    >
      {icon}
    </button>
  );
}

function readSidebarCollapsed() {
  try {
    return localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === 'true';
  } catch {
    return false;
  }
}

function saveSidebarCollapsed(collapsed: boolean) {
  try {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(collapsed));
  } catch {}
}

export default function AppSidebar() {
  const { t } = useTranslation();
  const { isDark, isUltra, toggleTheme, toggleUltra } = useTheme();
  const { open: openCommandPalette } = useCommandPalette();
  const navigate = useNavigate();
  const { pathname, hash } = useLocation();
  const { allSetting } = useAllSettings();
  const showSubFormats = !!(allSetting.subJsonEnable || allSetting.subClashEnable);
  const showSubBalancers = !!allSetting.subJsonEnable;

  // Desktop only. The sidebar starts expanded and is collapsed by an explicit
  // toggle, never by hovering: a hover-expanded overlay used to cover the page
  // it was next to and clipped the wordmark to "LxGa".
  const [collapsed, setCollapsed] = useState(readSidebarCollapsed);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const toggleCollapsed = useCallback(() => {
    setCollapsed((previous) => {
      const next = !previous;
      saveSidebarCollapsed(next);
      return next;
    });
  }, []);

  const currentTheme: 'light' | 'dark' = isDark ? 'dark' : 'light';
  const panelVersion = window.X_UI_CUR_VER || '';
  const monogram = BRAND_NAME.slice(0, 1).toUpperCase();

  const tabs = useMemo<{ key: string; icon: ComponentType; title: string }[]>(
    () => [
      ...PANEL_ROUTES.map((route) => ({
        key: route.path,
        icon: route.icon,
        title: t(route.titleKey),
      })),
      { key: LOGOUT_KEY, icon: LogoutOutlined, title: t('logout') },
    ],
    [t],
  );

  const navItems = useMemo(() => tabs.filter((tab) => tab.key !== LOGOUT_KEY), [tabs]);
  const utilItems = useMemo(() => tabs.filter((tab) => tab.key === LOGOUT_KEY), [tabs]);

  const settingsChildren = useMemo<NonNullable<MenuProps['items']>>(() => {
    const children: NonNullable<MenuProps['items']> = [
      {
        key: '/settings#general',
        icon: <SettingOutlined aria-hidden />,
        label: t('pages.settings.panelSettings'),
      },
      {
        key: '/settings#security',
        icon: <SafetyOutlined aria-hidden />,
        label: t('pages.settings.securitySettings'),
      },
      {
        key: '/settings#telegram',
        icon: <TelegramFilled aria-hidden />,
        label: t('pages.settings.TGBotSettings'),
      },
      {
        key: '/settings#email',
        icon: <MailOutlined aria-hidden />,
        label: t('pages.settings.emailSettings'),
      },
      {
        key: '/settings#discord',
        icon: <DiscordOutlined aria-hidden />,
        label: t('pages.settings.discordSettings'),
      },
      {
        key: '/settings#subscription',
        icon: <CloudServerOutlined aria-hidden />,
        label: t('pages.settings.subSettings'),
      },
    ];
    if (showSubFormats) {
      children.push({
        key: '/settings#subscription-formats',
        icon: <CodeOutlined aria-hidden />,
        label: t('menu.subFormats'),
      });
    }
    if (showSubBalancers) {
      children.push({
        key: '/settings#subscription-balancers',
        icon: <ApartmentOutlined aria-hidden />,
        label: t('pages.settings.subBalancers.menu'),
      });
    }
    return children;
  }, [t, showSubFormats, showSubBalancers]);

  const xrayChildren = useMemo<NonNullable<MenuProps['items']>>(
    () => [
      {
        key: '/xray#basic',
        icon: <SettingOutlined aria-hidden />,
        label: t('pages.xray.basicTemplate'),
      },
      {
        key: '/xray#balancer',
        icon: <ClusterOutlined aria-hidden />,
        label: t('pages.xray.Balancers'),
      },
      { key: '/xray#dns', icon: <DatabaseOutlined aria-hidden />, label: 'DNS' },
      {
        key: '/xray#advanced',
        icon: <CodeOutlined aria-hidden />,
        label: t('pages.xray.advancedTemplate'),
      },
    ],
    [t],
  );

  const settingsActive = pathname === '/settings';
  const xrayActive = pathname === '/xray';
  const selectedKey = settingsActive
    ? `/settings${hash || '#general'}`
    : xrayActive
      ? `/xray${hash || '#basic'}`
      : pathname === ''
        ? '/'
        : pathname;

  const openSubmenu = settingsActive ? '/settings' : xrayActive ? '/xray' : null;
  const [openKeys, setOpenKeys] = useState<string[]>(() => (openSubmenu ? [openSubmenu] : []));
  if (openSubmenu && !openKeys.includes(openSubmenu)) {
    setOpenKeys([...openKeys, openSubmenu]);
  }

  const toMenuItems = useCallback(
    (items: typeof tabs): MenuProps['items'] =>
      items.map((tab) => {
        const Icon = tab.icon;
        if (tab.key === '/settings') {
          return {
            key: tab.key,
            icon: <Icon aria-hidden />,
            label: tab.title,
            children: settingsChildren,
          };
        }
        if (tab.key === '/xray') {
          return {
            key: tab.key,
            icon: <Icon aria-hidden />,
            label: tab.title,
            children: xrayChildren,
          };
        }
        return { key: tab.key, icon: <Icon aria-hidden />, label: tab.title, title: '' };
      }),
    [settingsChildren, xrayChildren],
  );

  const openLink = useCallback(
    async (key: string) => {
      if (key === LOGOUT_KEY) {
        await HttpUtil.post('/logout');
        window.location.href = window.X_UI_BASE_PATH || '/';
        return;
      }
      navigate(key);
    },
    [navigate],
  );

  const onMenuClick = useCallback<NonNullable<MenuProps['onClick']>>(
    ({ key }) => {
      openLink(String(key));
    },
    [openLink],
  );

  const cycleTheme = useCallback(
    (id: string) => {
      pauseAnimationsUntilLeave(id);
      if (!isDark) {
        toggleTheme();
        if (isUltra) toggleUltra();
      } else if (!isUltra) {
        toggleUltra();
      } else {
        toggleUltra();
        toggleTheme();
      }
    },
    [isDark, isUltra, toggleTheme, toggleUltra],
  );

  // Keep the rail honest when the viewport shrinks to the drawer breakpoint and
  // back, without writing the drawer's state into the desktop preference.
  useEffect(() => {
    const onResize = () => {
      if (window.innerWidth > 768) setDrawerOpen(false);
    };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const collapseLabel = collapsed ? t('menu.expandSidebar') : t('menu.collapseSidebar');

  return (
    <div className={`ant-sidebar${collapsed ? ' sidebar-collapsed' : ''}`}>
      <Layout.Sider
        theme={currentTheme}
        width={SIDER_WIDTH}
        collapsedWidth={RAIL_WIDTH}
        collapsed={collapsed}
        trigger={null}
      >
        <div className={`sider-brand${collapsed ? ' is-rail' : ''}`}>
          {!collapsed && <span className="brand-text">{BRAND_NAME}</span>}
          <Tooltip title={collapseLabel} placement="right">
            {/* In the rail the monogram doubles as the expand button: one control
                instead of a badge squeezed next to a hamburger in 76px. */}
            <button
              type="button"
              className={`sidebar-collapse${collapsed ? ' is-mark' : ''}`}
              aria-label={collapseLabel}
              aria-expanded={!collapsed}
              aria-controls="sider-nav"
              onClick={toggleCollapsed}
            >
              {collapsed ? (
                <span className="brand-mark" aria-hidden>
                  {monogram}
                </span>
              ) : (
                <MenuFoldOutlined aria-hidden />
              )}
            </button>
          </Tooltip>
        </div>

        <Tooltip title={collapsed ? t('commandPalette.title') : undefined} placement="right">
          <button
            type="button"
            className={`sidebar-command-trigger${collapsed ? ' collapsed' : ''}`}
            onClick={openCommandPalette}
            aria-label={t('commandPalette.title') || 'Command Palette (Ctrl + K)'}
          >
            <span className="sidebar-command-left">
              <SearchOutlined aria-hidden className="sidebar-command-icon" />
              <span className="sidebar-command-text">
                {t('commandPalette.search') || 'Search...'}
              </span>
            </span>
            <span className="sidebar-command-kbd">
              <span className="kbd-cmd">{SHORTCUT_MODIFIER}</span>
              <span className="kbd-key">K</span>
            </span>
          </button>
        </Tooltip>

        <Menu
          id="sider-nav"
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          openKeys={collapsed ? undefined : openKeys}
          onOpenChange={(keys) => setOpenKeys(keys as string[])}
          className="sider-nav"
          items={toMenuItems(navItems)}
          onClick={onMenuClick}
        />
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="sider-utility"
          items={toMenuItems(utilItems)}
          onClick={onMenuClick}
        />

        <div className="sider-footer">
          <VersionBadge version={panelVersion} collapsed={collapsed} />
          <div className="sider-tools">
            <DocsButton ariaLabel={t('menu.docs') || 'Documentation'} />
            <ThemeCycleButton
              id="theme-cycle"
              isDark={isDark}
              isUltra={isUltra}
              onCycle={() => cycleTheme('theme-cycle')}
              ariaLabel={t('menu.theme')}
            />
          </div>
        </div>
      </Layout.Sider>

      <Drawer
        placement="left"
        closable={false}
        open={drawerOpen}
        rootClassName={currentTheme}
        size="min(82vw, 320px)"
        styles={{
          wrapper: { padding: 0 },
          body: { padding: 0, display: 'flex', flexDirection: 'column', height: '100%' },
          header: { display: 'none' },
        }}
        onClose={() => setDrawerOpen(false)}
      >
        <div className="drawer-header">
          <div className="brand-block">
            <span className="drawer-brand">{BRAND_NAME}</span>
          </div>
          <div className="drawer-header-actions">
            <DocsButton ariaLabel={t('menu.docs') || 'Documentation'} />
            <ThemeCycleButton
              id="theme-cycle-drawer"
              isDark={isDark}
              isUltra={isUltra}
              onCycle={() => cycleTheme('theme-cycle-drawer')}
              ariaLabel={t('menu.theme')}
            />
            <button
              className="drawer-close"
              type="button"
              aria-label={t('close')}
              onClick={() => setDrawerOpen(false)}
            >
              <CloseOutlined aria-hidden />
            </button>
          </div>
        </div>
        <button
          type="button"
          className="sidebar-command-trigger"
          onClick={() => {
            setDrawerOpen(false);
            openCommandPalette();
          }}
          aria-label={t('commandPalette.title') || 'Command Palette (Ctrl + K)'}
          style={{ margin: '8px 12px 4px', width: 'calc(100% - 24px)' }}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <SearchOutlined aria-hidden className="sidebar-command-icon" />
            <span>{t('commandPalette.search') || 'Search...'}</span>
          </span>
          <span className="sidebar-command-kbd">
            <span className="kbd-cmd">{SHORTCUT_MODIFIER}</span>
            <span className="kbd-key">K</span>
          </span>
        </button>
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          openKeys={openKeys}
          onOpenChange={(keys) => setOpenKeys(keys as string[])}
          className="drawer-menu drawer-nav"
          items={toMenuItems(navItems)}
          onClick={(info) => {
            onMenuClick(info);
            setDrawerOpen(false);
          }}
        />
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="drawer-menu drawer-utility"
          items={toMenuItems(utilItems)}
          onClick={(info) => {
            onMenuClick(info);
            setDrawerOpen(false);
          }}
        />
        <div className="drawer-footer">
          <VersionBadge version={panelVersion} />
        </div>
      </Drawer>

      {!drawerOpen && (
        <button
          className="drawer-handle"
          type="button"
          aria-label={t('menu.openMenu')}
          onClick={() => setDrawerOpen(true)}
        >
          <MenuOutlined aria-hidden />
        </button>
      )}
    </div>
  );
}
