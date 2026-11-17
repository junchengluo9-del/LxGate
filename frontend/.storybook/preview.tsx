import { useLayoutEffect } from 'react';
import type { Decorator, Preview } from '@storybook/react-vite';
import { ConfigProvider } from 'antd';
import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';

import { buildAntdThemeConfig } from '@/hooks/useTheme';
import { INTERPOLATION } from '@/i18n/react';
import enUS from '../../internal/web/translation/en-US.json';

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    lng: 'en-US',
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: enUS } },
    interpolation: INTERPOLATION,
    returnNull: false,
  });
}

export const withTheme: Decorator = (Story, context) => {
  const dark = context.globals.theme === 'dark';
  useLayoutEffect(() => {
    // The palette variables are read by `html` too (its background shows below
    // a short story and on overscroll), so the theme class goes on both.
    const theme = dark ? 'dark' : 'light';
    document.documentElement.classList.remove('dark', 'light');
    document.documentElement.classList.add(theme);
    document.body.classList.remove('dark', 'light');
    document.body.classList.add(theme);
    document.documentElement.style.colorScheme = theme;
    document.documentElement.removeAttribute('data-theme');
  }, [dark]);
  return (
    // The click wave outlives its story and re-renders from a ResizeObserver
    // inside the next story's act(), tripping React's act-environment warning.
    <ConfigProvider theme={buildAntdThemeConfig(dark, false)} wave={{ disabled: true }}>
      <div style={{ padding: 24, minWidth: 320 }}>
        <Story />
      </div>
    </ConfigProvider>
  );
};

const preview: Preview = {
  decorators: [withTheme],
  globalTypes: {
    theme: {
      description: 'Ant Design theme',
      defaultValue: 'light',
      toolbar: {
        title: 'Theme',
        icon: 'circlehollow',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
  parameters: {
    controls: {
      expanded: true,
      sort: 'requiredFirst',
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    a11y: {
      test: 'error',
    },
  },
};

export default preview;
