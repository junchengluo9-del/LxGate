import { createContext, useCallback, useContext, useLayoutEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { theme as antdTheme } from 'antd';
import type { ThemeConfig } from 'antd';

const STORAGE_DARK = 'dark-mode';
const STORAGE_ULTRA = 'isUltraDarkThemeEnabled';

function readBool(key: string, fallback: boolean): boolean {
  const raw = localStorage.getItem(key);
  if (raw === null) return fallback;
  return raw === 'true';
}

function applyDom(isDark: boolean, isUltra: boolean) {
  // The palette variables are read by `html` itself (its background is what
  // shows below a short page and while overscrolling), so the theme class has
  // to be on the document element as well as on the body.
  const theme = isDark ? 'dark' : 'light';
  document.documentElement.classList.remove('dark', 'light');
  document.documentElement.classList.add(theme);
  document.body.classList.remove('dark', 'light');
  document.body.classList.add(theme);
  // Native scrollbars read color-scheme, not the body class.
  document.documentElement.style.colorScheme = isDark ? 'dark' : 'light';
  if (isUltra) {
    document.documentElement.setAttribute('data-theme', 'ultra-dark');
  } else {
    document.documentElement.removeAttribute('data-theme');
  }
  const msg = document.getElementById('message');
  if (msg) {
    msg.classList.remove('dark', 'light');
    msg.classList.add(isDark ? 'dark' : 'light');
  }
}

// module load so the document is in the right theme before React mounts.
const initialDark = readBool(STORAGE_DARK, true);
const initialUltra = readBool(STORAGE_ULTRA, false);
applyDom(initialDark, initialUltra);

const DARK_TOKENS = {
  // Liquid Glass 2.0 dark canvas / surface pair. The canvas is near-black with a
  // blue cast so the frosted sidebar and status capsule read as glass rather
  // than grey panels.
  colorBgBase: '#090B10',
  colorBgLayout: '#090B10',
  colorBgContainer: '#12151E',
  colorBgElevated: '#161A24',
  colorBorder: 'rgba(255, 255, 255, 0.08)',
  colorBorderSecondary: 'rgba(255, 255, 255, 0.08)',
  borderRadius: 12,
  borderRadiusLG: 16,
  borderRadiusSM: 8,
  colorPrimary: '#4C8DFF',
};
const ULTRA_DARK_TOKENS = {
  colorBgBase: '#000',
  colorBgLayout: '#000',
  colorBgContainer: '#101013',
  colorBgElevated: '#1A1A1E',
  colorBorder: 'rgba(255, 255, 255, 0.06)',
  colorBorderSecondary: 'rgba(255, 255, 255, 0.04)',
  borderRadius: 12,
  borderRadiusLG: 16,
  borderRadiusSM: 8,
  colorPrimary: '#4C8DFF',
};
const DARK_LAYOUT_TOKENS = {
  bodyBg: 'transparent',
  headerBg: 'transparent',
  headerColor: '#ffffff',
  footerBg: 'transparent',
  siderBg: 'rgba(18, 21, 30, 0.72)',
  triggerBg: '#12151E',
  triggerColor: '#ffffff',
};
const ULTRA_DARK_LAYOUT_TOKENS = {
  bodyBg: 'transparent',
  headerBg: 'transparent',
  headerColor: '#ffffff',
  footerBg: 'transparent',
  siderBg: 'rgba(16, 16, 19, 0.78)',
  triggerBg: '#101013',
  triggerColor: '#ffffff',
};
const DARK_MENU_TOKENS = {
  darkItemBg: 'transparent',
  darkSubMenuItemBg: 'rgba(255, 255, 255, 0.02)',
  darkPopupBg: '#161A24',
  itemBorderRadius: 8,
  itemSelectedBg: 'rgba(76, 141, 255, 0.18)',
  itemSelectedColor: '#DCE6FF',
};
const ULTRA_DARK_MENU_TOKENS = {
  darkItemBg: 'transparent',
  darkSubMenuItemBg: 'rgba(255, 255, 255, 0.02)',
  darkPopupBg: '#1A1A1E',
  itemBorderRadius: 8,
  itemSelectedBg: 'rgba(76, 141, 255, 0.2)',
  itemSelectedColor: '#DCE6FF',
};
const DARK_CARD_TOKENS = {
  colorBorderSecondary: 'rgba(255, 255, 255, 0.08)',
  borderRadiusLG: 16,
};
const ULTRA_DARK_CARD_TOKENS = {
  colorBorderSecondary: 'rgba(255, 255, 255, 0.05)',
  borderRadiusLG: 16,
};
const STATISTIC_TOKENS = {
  contentFontSize: 17,
  titleFontSize: 11,
};
const LIGHT_CONTRAST_TOKENS = {
  colorTextDescription: 'rgba(0, 0, 0, 0.58)',
  colorTextTertiary: 'rgba(0, 0, 0, 0.58)',
  colorTextPlaceholder: '#767676',
  colorError: '#cf1322',
  colorErrorText: '#cf1322',
  colorSuccessText: '#237804',
};
const LIGHT_BUTTON_TOKENS = {
  colorPrimary: '#0958d9',
  colorPrimaryHover: '#2468e5',
  colorPrimaryActive: '#073ea8',
};

// Liquid Glass 2.0 light palette: a #FAFBFC canvas with translucent white cards.
// It is declared after the contrast tokens because it spreads them.
const LIGHT_TOKENS = {
  ...LIGHT_CONTRAST_TOKENS,
  colorBgBase: '#FAFBFC',
  colorBgLayout: '#FAFBFC',
  colorBgContainer: 'rgba(255, 255, 255, 0.82)',
  colorBgElevated: 'rgba(255, 255, 255, 0.96)',
  colorBorder: 'rgba(15, 23, 42, 0.08)',
  colorBorderSecondary: 'rgba(15, 23, 42, 0.08)',
  borderRadius: 12,
  borderRadiusLG: 16,
  borderRadiusSM: 8,
};

// hashed:false drops the `:where(.css-<hash>)` wrapper antd puts around every
// rule. It costs nothing in specificity — `:where()` contributes zero, so the
// panel's own `.ant-*` overrides still win — and it removes roughly 5,700
// wrappers, 16% of the generated stylesheet, from what the browser has to parse.
//
// cssVar.key pins the CSS-variable scope. Every panel page mounts its own
// ConfigProvider (there is no root one), and without a fixed key each mints a
// fresh useId-derived scope, so navigating re-serialises and re-injects the whole
// token block under a new class instead of reusing the one already in the head.
const SHARED_STYLE_CONFIG = {
  hashed: false,
  cssVar: { key: 'xui' },
} as const;

export function buildAntdThemeConfig(isDark: boolean, isUltra: boolean): ThemeConfig {
  if (!isDark) {
    return {
      ...SHARED_STYLE_CONFIG,
      algorithm: antdTheme.defaultAlgorithm,
      token: LIGHT_TOKENS,
      components: {
        Statistic: STATISTIC_TOKENS,
        Card: { borderRadiusLG: 16 },
        Layout: {
          bodyBg: 'transparent',
          headerBg: 'transparent',
          siderBg: 'rgba(255, 255, 255, 0.82)',
          triggerBg: 'rgba(255, 255, 255, 0.9)',
        },
        Menu: {
          itemBg: 'transparent',
          subMenuItemBg: 'rgba(15, 23, 42, 0.02)',
          itemBorderRadius: 8,
          itemSelectedBg: 'rgba(9, 88, 217, 0.1)',
          itemSelectedColor: '#0958d9',
        },
        Table: { borderRadiusLG: 12, headerBg: 'transparent' },
        Input: { borderRadius: 8 },
        Select: { borderRadius: 8 },
        Button: { ...LIGHT_BUTTON_TOKENS, borderRadius: 8 },
        Drawer: { colorBgElevated: 'rgba(255, 255, 255, 0.9)' },
        Modal: { contentBg: 'rgba(255, 255, 255, 0.94)' },
      },
    };
  }
  return {
    ...SHARED_STYLE_CONFIG,
    algorithm: antdTheme.darkAlgorithm,
    token: isUltra ? ULTRA_DARK_TOKENS : DARK_TOKENS,
    components: {
      Layout: isUltra ? ULTRA_DARK_LAYOUT_TOKENS : DARK_LAYOUT_TOKENS,
      Menu: isUltra ? ULTRA_DARK_MENU_TOKENS : DARK_MENU_TOKENS,
      Card: isUltra ? ULTRA_DARK_CARD_TOKENS : DARK_CARD_TOKENS,
      Statistic: STATISTIC_TOKENS,
      Table: { borderRadiusLG: 12, headerBg: 'transparent', colorBgContainer: 'transparent' },
      Input: { borderRadius: 8 },
      Select: { borderRadius: 8 },
      Button: { borderRadius: 8 },
      Drawer: { colorBgElevated: isUltra ? 'rgba(16, 16, 19, 0.86)' : 'rgba(18, 21, 30, 0.82)' },
      Modal: { contentBg: isUltra ? 'rgba(16, 16, 19, 0.9)' : 'rgba(18, 21, 30, 0.88)' },
    },
  };
}

export function pauseAnimationsUntilLeave(elementId: string): void {
  document.documentElement.setAttribute('data-theme-animations', 'off');
  const el = document.getElementById(elementId);
  if (!el) return;
  const restore = () => {
    document.documentElement.removeAttribute('data-theme-animations');
    el.removeEventListener('mouseleave', restore);
    el.removeEventListener('touchend', restore);
  };
  el.addEventListener('mouseleave', restore);
  el.addEventListener('touchend', restore);
}

interface ThemeContextValue {
  isDark: boolean;
  isUltra: boolean;
  toggleTheme: () => void;
  toggleUltra: () => void;
  antdThemeConfig: ThemeConfig;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [isDark, setIsDark] = useState<boolean>(initialDark);
  const [isUltra, setIsUltra] = useState<boolean>(initialUltra);

  useLayoutEffect(() => {
    applyDom(isDark, isUltra);
    localStorage.setItem(STORAGE_DARK, String(isDark));
    localStorage.setItem(STORAGE_ULTRA, String(isUltra));
  }, [isDark, isUltra]);

  const toggleTheme = useCallback(() => setIsDark((v) => !v), []);
  const toggleUltra = useCallback(() => setIsUltra((v) => !v), []);

  const antdThemeConfig = useMemo(() => buildAntdThemeConfig(isDark, isUltra), [isDark, isUltra]);

  const value = useMemo<ThemeContextValue>(
    () => ({ isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig }),
    [isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used inside <ThemeProvider>');
  return ctx;
}
