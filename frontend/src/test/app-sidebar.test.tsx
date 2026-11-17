import { act, fireEvent, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, expect, test, vi } from 'vitest';

import AppSidebar from '@/layouts/AppSidebar';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/queries/useAllSettings', () => ({
  useAllSettings: () => ({ allSetting: {} }),
}));

afterEach(() => {
  localStorage.clear();
});

// rc-menu registers its items in a microtask after render; settle it inside act().
async function renderSidebar() {
  const view = renderWithProviders(
    <MemoryRouter>
      <AppSidebar />
    </MemoryRouter>,
  );
  await act(async () => {});
  return view;
}

test('starts expanded so the full wordmark and labels are visible', async () => {
  const view = await renderSidebar();
  const sider = view.container.querySelector('.ant-layout-sider');
  const wrapper = view.container.querySelector('.ant-sidebar');

  expect(sider?.classList.contains('ant-layout-sider-collapsed')).toBe(false);
  expect(view.container.querySelector('.brand-text')?.textContent).toBe('LxGate');
  expect(screen.getByRole('button', { name: 'Collapse sidebar' })).not.toBeNull();
  // The wrapper only ever matches its own sider, so the content beside it is
  // pushed instead of covered by a floating panel.
  expect(wrapper?.getAttribute('class')).not.toContain('sidebar-pinned');
});

test('collapses from the toggle, remembers the choice and never reacts to hover', async () => {
  const first = await renderSidebar();
  const sider = first.container.querySelector('.ant-layout-sider');
  const wrapper = first.container.querySelector('.ant-sidebar');

  fireEvent.mouseEnter(wrapper!);
  expect(sider?.classList.contains('ant-layout-sider-collapsed')).toBe(false);

  fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' }));
  expect(sider?.classList.contains('ant-layout-sider-collapsed')).toBe(true);
  expect(localStorage.getItem('sidebar-collapsed')).toBe('true');
  // The rail shows a monogram instead of the clipped wordmark.
  expect(first.container.querySelector('.brand-mark')?.textContent).toBe('L');
  expect(first.container.querySelector('.brand-text')).toBeNull();
  expect(screen.getByRole('button', { name: 'Expand sidebar' })).not.toBeNull();

  first.unmount();

  const second = await renderSidebar();
  const restored = second.container.querySelector('.ant-layout-sider');
  expect(restored?.classList.contains('ant-layout-sider-collapsed')).toBe(true);

  fireEvent.click(screen.getByRole('button', { name: 'Expand sidebar' }));
  expect(restored?.classList.contains('ant-layout-sider-collapsed')).toBe(false);
  expect(localStorage.getItem('sidebar-collapsed')).toBe('false');
  expect(second.container.querySelector('.brand-text')?.textContent).toBe('LxGate');
});

test('labels the palette shortcut with the modifier the platform actually uses', async () => {
  const view = await renderSidebar();
  const chip = view.container.querySelector('.sidebar-command-kbd');
  expect(chip?.textContent).toBe('CtrlK');
});
