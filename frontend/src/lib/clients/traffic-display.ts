import { ColorUtils } from '@/utils';

export interface TrafficDisplayInput {
  up: number;
  down: number;
  total: number;
  enabled: boolean;
  trafficDiff: number;
}

export interface TrafficDisplay {
  used: number;
  remaining: number;
  percent: number;
  isUnlimited: boolean;
  isDepleted: boolean;
  strokeColor: string;
  status: 'normal' | 'exception' | undefined;
}

const DISABLED_STROKE = {
  light: '#bcbcbc',
  dark: 'rgb(72, 84, 105)',
} as const;

const UNLIMITED_STROKE = '#722ed1';

export function computeTrafficDisplay(input: TrafficDisplayInput, isDark: boolean): TrafficDisplay {
  const up = input.up || 0;
  const down = input.down || 0;
  const used = up + down;
  const total = input.total || 0;
  const isUnlimited = total <= 0;

  // An unlimited client has no quota to fill: the track stays empty and the ∞
  // icon in the limit column carries the meaning. A 100% bar read as "quota
  // used up" at 0 B, and the inbounds page already shows "0 B / ∞" for the
  // same client.
  const percent = isUnlimited ? 0 : Math.min(100, Math.max(0, (used / total) * 100));

  const isDepleted = !isUnlimited && used >= total;
  const remaining = isUnlimited ? 0 : Math.max(0, total - used);

  let strokeColor: string;
  if (!input.enabled) {
    strokeColor = isDark ? DISABLED_STROKE.dark : DISABLED_STROKE.light;
  } else if (isUnlimited) {
    strokeColor = UNLIMITED_STROKE;
  } else {
    strokeColor = ColorUtils.clientUsageColor({ up, down, total }, input.trafficDiff);
  }

  return {
    used,
    remaining,
    percent,
    isUnlimited,
    isDepleted,
    strokeColor,
    status: isDepleted && input.enabled ? 'exception' : undefined,
  };
}
