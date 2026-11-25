import type { ReactNode } from 'react';
import { Button } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import './ListEmpty.css';

interface ListEmptyProps {
  /** The page icon, shown in a soft badge above the text. */
  icon: ReactNode;
  /** Label of the page's existing add action; the button only reuses it. */
  actionLabel?: string;
  onAction?: () => void;
}

/**
 * Empty-table state shared by the list pages. It carries its own stylesheet, so
 * it looks the same on a direct refresh as after visiting another page (the old
 * `.card-empty` rule lived in lazily-loaded page CSS and rendered faint on
 * /groups). The button calls the very handler the toolbar's add button calls.
 */
export default function ListEmpty({ icon, actionLabel, onAction }: ListEmptyProps) {
  const { t } = useTranslation();
  return (
    <div className="list-empty">
      <span className="list-empty-icon" aria-hidden>
        {icon}
      </span>
      <div className="list-empty-title">{t('emptyState.title')}</div>
      <div className="list-empty-hint">{t('emptyState.hint')}</div>
      {onAction && actionLabel && (
        <Button type="primary" ghost icon={<PlusOutlined aria-hidden />} onClick={onAction}>
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
