import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';

/** Who decides which inbounds leave through the VPNGate exit. */
export type VpngateTakeoverMode = 'all' | 'selected' | 'manual';

export interface VpngateInboundOption {
  id: number;
  tag: string;
  remark?: string;
  protocol?: string;
  port?: number;
  enable?: boolean;
  nodeId?: number | null;
}

export interface VpngateTakeoverState {
  mode: VpngateTakeoverMode;
  inboundTags: string[];
  /** Whether the vpngate-out socks outbound is registered in the Xray template. */
  bound?: boolean;
  ruleTag?: string;
  /** Position of the panel-managed rule in routing.rules, -1 when absent. */
  ruleIndex?: number;
  inbounds: VpngateInboundOption[];
  proxyAddress?: string;
  openvpnDevice?: string;
  changed?: boolean;
}

/**
 * The takeover state is derived from the Xray template on the server, so every
 * page that shows an exit badge reads the same answer instead of guessing from
 * a local copy.
 */
export function useVpngateTakeoverQuery() {
  return useQuery({
    queryKey: ['vpngate', 'takeover'],
    queryFn: async (): Promise<VpngateTakeoverState> => {
      const msg = await HttpUtil.get<VpngateTakeoverState>(
        '/panel/api/vpngate/takeover',
        undefined,
        {
          silent: true,
        },
      );
      if (!msg?.success) throw new Error(msg?.msg || 'takeover state unavailable');
      return {
        mode: (msg.obj?.mode ?? 'manual') as VpngateTakeoverMode,
        inboundTags: msg.obj?.inboundTags ?? [],
        bound: !!msg.obj?.bound,
        ruleTag: msg.obj?.ruleTag,
        ruleIndex: msg.obj?.ruleIndex ?? -1,
        inbounds: msg.obj?.inbounds ?? [],
        proxyAddress: msg.obj?.proxyAddress,
        openvpnDevice: msg.obj?.openvpnDevice,
      };
    },
    staleTime: 15_000,
    retry: false,
  });
}

export interface SetVpngateTakeoverInput {
  mode: VpngateTakeoverMode;
  inboundTags: string[];
}

/** Applies a takeover mode; the server rewrites the managed rule and reloads Xray. */
export function useSetVpngateTakeover() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: SetVpngateTakeoverInput): Promise<VpngateTakeoverState> => {
      const msg = await HttpUtil.post<VpngateTakeoverState>('/panel/api/vpngate/takeover', input, {
        silent: true,
      });
      if (!msg?.success) throw new Error(msg?.msg || 'takeover failed');
      return msg.obj as VpngateTakeoverState;
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['vpngate'] });
      queryClient.invalidateQueries({ queryKey: ['inbounds-list'] });
    },
  });
}

/** True when an inbound's traffic leaves through the VPNGate exit under state. */
export function inboundUsesVpngate(
  state: VpngateTakeoverState | undefined,
  tag: string | undefined,
) {
  if (!state || !tag) return false;
  if (state.mode === 'all') return true;
  if (state.mode === 'selected') return state.inboundTags.includes(tag);
  return false;
}
