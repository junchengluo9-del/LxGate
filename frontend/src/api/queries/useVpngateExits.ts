import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { postVpngateJson, type VpngateInboundOption } from '@/api/queries/useVpngateTakeover';

/**
 * Multi-exit state as the panel sees it: the engine's exit slots, the host
 * memory budget behind the admission check, and the per-inbound binding that
 * the Xray template actually carries.
 */
export interface VpngateExitNode {
  id?: string;
  country?: string;
  country_short?: string;
  ip?: string;
  host_name?: string;
  ip_type?: string;
  latency_ms?: number;
}

export interface VpngateExitRouting {
  routing_mode?: 'auto' | 'fixed_region' | 'fixed_ip' | 'favorites';
  force_country?: string;
  routing_ip_type?: string;
  fav_fail_fallback?: boolean;
  favorite_node_ids?: string[];
  fixed_node_id?: string;
  connection_enabled?: boolean;
}

export interface VpngateExit {
  id: string;
  slot: number;
  name: string;
  /** Tunnel device, e.g. lxgtun0. */
  dev: string;
  /** Policy-routing table, e.g. 100. */
  table: number;
  /** Loopback SOCKS/HTTP port, e.g. 7929. */
  proxyPort: number;
  proxyAddress?: string;
  socksUrl?: string;
  outboundTag?: string;
  routing: VpngateExitRouting;
  routingLabel?: string;
  nodeId?: string;
  node?: VpngateExitNode;
  running: boolean;
  connecting?: boolean;
  stage?: string;
  stageDetail?: string;
  lastError?: string;
  latencyMs?: number;
  proxyOk?: boolean | null;
  proxyIp?: string;
  proxyLatencyMs?: number;
  proxyError?: string;
  connectedAt?: number;
  createdAt?: number;
  isDefault?: boolean;
}

export interface VpngateMemory {
  totalMb: number;
  availableMb: number;
  reserveMb: number;
  perTunnelMb: number;
  perTunnelSource?: 'measured' | 'baseline';
  activeExits: number;
  configuredExits: number;
  headroomMb: number;
  canStartNew: boolean;
  maxActiveExits: number;
  maxSlots: number;
}

export interface VpngateExitsPayload {
  exits: VpngateExit[];
  memory?: VpngateMemory;
  /** inbound tag -> exit slot (absent = direct). */
  bindings?: Record<string, number>;
  registeredSlots?: number[];
  templateConfigured?: boolean;
  inbounds?: VpngateInboundOption[];
  engineError?: string;
}

const EXITS_URL = '/panel/api/vpngate/exits';

export function useVpngateExitsQuery(enabled = true) {
  return useQuery({
    queryKey: ['vpngate', 'exits'],
    queryFn: async (): Promise<VpngateExitsPayload> => {
      const msg = await HttpUtil.get<VpngateExitsPayload>(EXITS_URL, undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'exit list unavailable');
      return {
        exits: msg.obj?.exits ?? [],
        memory: msg.obj?.memory,
        bindings: msg.obj?.bindings ?? {},
        registeredSlots: msg.obj?.registeredSlots ?? [],
        templateConfigured: msg.obj?.templateConfigured,
        inbounds: msg.obj?.inbounds ?? [],
        engineError: msg.obj?.engineError,
      };
    },
    refetchInterval: 5000,
    staleTime: 3000,
    retry: false,
    enabled,
  });
}

function useExitMutation<Input>(post: (input: Input) => Promise<unknown>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: post,
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['vpngate'] });
      queryClient.invalidateQueries({ queryKey: ['inbounds-list'] });
    },
  });
}

/** Creates one exit slot; the engine assigns its device, table and proxy port. */
export function useCreateVpngateExit() {
  return useExitMutation(async (name: string) => {
    const msg = await postVpngateJson(EXITS_URL, { name });
    if (!msg?.success) throw new Error(msg?.msg || 'create failed');
    return msg;
  });
}

export interface UpdateExitInput {
  id: string;
  name?: string;
  routing_mode?: string;
  force_country?: string;
  routing_ip_type?: string;
  fav_fail_fallback?: boolean;
}

/** Renames an exit and/or changes its own routing policy. */
export function useUpdateVpngateExit() {
  return useExitMutation(async (input: UpdateExitInput) => {
    const msg = await postVpngateJson(`${EXITS_URL}/update`, input);
    if (!msg?.success) throw new Error(msg?.msg || 'update failed');
    return msg;
  });
}

export function useDeleteVpngateExit() {
  return useExitMutation(async (id: string) => {
    const msg = await postVpngateJson(`${EXITS_URL}/delete`, { id });
    if (!msg?.success) throw new Error(msg?.msg || 'delete failed');
    return msg;
  });
}

export interface ConnectExitInput {
  exitId: string;
  /** Omitted: let the exit pick a node its saved policy accepts. */
  nodeId?: string;
}

export function useConnectVpngateExit() {
  return useExitMutation(async ({ exitId, nodeId }: ConnectExitInput) => {
    const url = nodeId ? `${EXITS_URL}/connect` : `${EXITS_URL}/auto-connect`;
    const msg = await postVpngateJson(url, nodeId ? { exitId, nodeId } : { exitId });
    if (!msg?.success) throw new Error(msg?.msg || 'connect failed');
    return msg;
  });
}

export function useDisconnectVpngateExit() {
  return useExitMutation(async (exitId: string) => {
    const msg = await postVpngateJson(`${EXITS_URL}/disconnect`, { exitId });
    if (!msg?.success) throw new Error(msg?.msg || 'disconnect failed');
    return msg;
  });
}

/** Binds one inbound to one exit (empty exitId = direct). */
export function useBindVpngateExit() {
  return useExitMutation(
    async ({ inboundTag, exitId }: { inboundTag: string; exitId?: string }) => {
      const msg = await postVpngateJson(`${EXITS_URL}/bind`, {
        inboundTag,
        exitId: exitId ?? '',
      });
      if (!msg?.success) throw new Error(msg?.msg || 'bind failed');
      return msg;
    },
  );
}

/** The exit one inbound leaves through, or undefined when it goes direct. */
export function exitForInbound(
  payload: VpngateExitsPayload | undefined,
  tag: string | undefined,
): VpngateExit | undefined {
  if (!payload || !tag) return undefined;
  const slot = payload.bindings?.[tag];
  if (!slot) return undefined;
  return payload.exits.find((exit) => exit.slot === slot);
}
