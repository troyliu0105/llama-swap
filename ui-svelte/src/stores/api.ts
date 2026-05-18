import { writable } from "svelte/store";
import type {
  Model,
  ActivityLogEntry,
  VersionInfo,
  LogData,
  APIEventEnvelope,
  ReqRespCapture,
  InFlightStats,
  PerformanceResponse,
} from "../lib/types";
import { connectionState } from "./theme";
import { currentRoute } from "./route";

const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

// Stores
export const models = writable<Model[]>([]);
export const proxyLogs = writable<string>("");
export const upstreamLogs = writable<string>("");
export const metrics = writable<ActivityLogEntry[]>([]);
export const inFlightRequests = writable<number>(0);
export const codexAccounts = writable<CodexAccount[]>([]);
export const versionInfo = writable<VersionInfo>({
  build_date: "unknown",
  commit: "unknown",
  version: "unknown",
});

let apiEventSource: EventSource | null = null;

function appendLog(newData: string, store: typeof proxyLogs | typeof upstreamLogs): void {
  store.update((prev) => {
    const updatedLog = prev + newData;
    return updatedLog.length > LOG_LENGTH_LIMIT ? updatedLog.slice(-LOG_LENGTH_LIMIT) : updatedLog;
  });
}

export function enableAPIEvents(enabled: boolean): void {
  if (!enabled) {
    apiEventSource?.close();
    apiEventSource = null;
    metrics.set([]);
    inFlightRequests.set(0);
    return;
  }

  let retryCount = 0;
  const initialDelay = 1000; // 1 second

  const connect = () => {
    apiEventSource?.close();
    apiEventSource = new EventSource("/api/events");

    connectionState.set("connecting");

    apiEventSource.onopen = () => {
      // Clear everything on connect to keep things in sync
      proxyLogs.set("");
      upstreamLogs.set("");
      metrics.set([]);
      inFlightRequests.set(0);
      models.set([]);
      retryCount = 0;
      connectionState.set("connected");

      fetchActivityHistory().then((history) => {
        if (history.length > 0) {
          metrics.set(history);
        }
      });
    };

    apiEventSource.onmessage = (e: MessageEvent) => {
      try {
        const message = JSON.parse(e.data) as APIEventEnvelope;
        switch (message.type) {
          case "modelStatus": {
            const newModels = JSON.parse(message.data) as Model[];
            // Sort models by name and id
            newModels.sort((a, b) => {
              return (a.name + a.id).localeCompare(b.name + b.id, undefined, { numeric: true });
            });
            models.set(newModels);
            break;
          }

          case "logData": {
            const logData = JSON.parse(message.data) as LogData;
            switch (logData.source) {
              case "proxy":
                appendLog(logData.data, proxyLogs);
                break;
              case "upstream":
                appendLog(logData.data, upstreamLogs);
                break;
            }
            break;
          }

          case "metrics": {
            const newMetrics = JSON.parse(message.data) as ActivityLogEntry[];
            metrics.update((prevMetrics) => [...newMetrics, ...prevMetrics]);
            break;
          }
          case "inflight": {
            const stats = JSON.parse(message.data) as InFlightStats;
            inFlightRequests.set(stats.total ?? 0);
            break;
          }
          case "codexQuota": {
            let route: string;
            currentRoute.subscribe((r) => (route = r))();
            if (route !== "/codex") break;
            fetchCodexAccounts().then((accounts) => {
              if (accounts.length > 0) {
                codexAccounts.set(accounts);
              }
            });
            break;
          }
        }
      } catch (err) {
        console.error(e.data, err);
      }
    };

    apiEventSource.onerror = () => {
      apiEventSource?.close();
      retryCount++;
      const delay = Math.min(initialDelay * Math.pow(2, retryCount - 1), 5000);
      connectionState.set("disconnected");
      setTimeout(connect, delay);
    };
  };

  connect();
}

// Fetch version info when connected
connectionState.subscribe(async (status) => {
  if (status === "connected") {
    try {
      const response = await fetch("/api/version");
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data: VersionInfo = await response.json();
      versionInfo.set(data);
    } catch (error) {
      console.error(error);
    }
  }
});

export async function listModels(): Promise<Model[]> {
  try {
    const response = await fetch("/api/models/");
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    const data = await response.json();
    return data || [];
  } catch (error) {
    console.error("Failed to fetch models:", error);
    return [];
  }
}

export async function unloadAllModels(): Promise<void> {
  try {
    const response = await fetch(`/api/models/unload`, {
      method: "POST",
    });
    if (!response.ok) {
      throw new Error(`Failed to unload models: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to unload models:", error);
    throw error;
  }
}

export async function unloadSingleModel(model: string): Promise<void> {
  try {
    const response = await fetch(`/api/models/unload/${model}`, {
      method: "POST",
    });
    if (!response.ok) {
      throw new Error(`Failed to unload model: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to unload model", model, error);
    throw error;
  }
}

export async function loadModel(model: string): Promise<void> {
  try {
    const response = await fetch(`/upstream/${model}/`, {
      method: "GET",
    });
    if (!response.ok) {
      throw new Error(`Failed to load model: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to load model:", error);
    throw error;
  }
}

export async function getCapture(id: number): Promise<ReqRespCapture | null> {
  try {
    const response = await fetch(`/api/captures/${id}`);
    if (response.status === 404) {
      return null;
    }
    if (!response.ok) {
      throw new Error(`Failed to fetch capture: ${response.status}`);
    }
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch capture:", error);
    return null;
  }
}

export interface AuditUser {
  id: number;
  api_key: string;
  name: string;
  created_at: string;
  total_requests: number;
  last_request_at: string;
}

export interface AuditUsageEntry {
  user_id: number;
  name: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
}

export interface AuditModelUsage {
  model: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  request_count: number;
}

export interface AuditCaptureInfo {
  id: number;
  session_id: number;
  model: string;
  req_path: string;
  seq_num: number;
  created_at: string;
}

export interface CodexWindowSnapshot {
  used_percent: number;
  reset_after_seconds: number;
  reset_at: number;
  window_minutes: number;
}

export interface CodexCreditsSnapshot {
  balance: string;
  has_credits: boolean;
  unlimited: boolean;
}

export interface CodexModelLimitSnapshot {
  limit_name: string;
  primary_over_secondary_limit_percent: number;
  primary_used_percent: number;
  primary_reset_after_seconds: number;
  primary_reset_at: number;
  primary_window_minutes: number;
  secondary_used_percent: number;
  secondary_reset_after_seconds: number;
  secondary_reset_at: number;
  secondary_window_minutes: number;
}

export interface CodexAccount {
  name: string;
  plan_type: string;
  active_limit: string;
  token_valid: boolean;
  token_expires_at: number;
  account_id: string;
  primary: CodexWindowSnapshot;
  secondary: CodexWindowSnapshot;
  credits: CodexCreditsSnapshot;
  stats: {
    name: string;
    totalRequests: number;
    cachedTokens: number;
    inputTokens: number;
    cacheHitRate: number;
  } | null;
  model_limits: Record<string, CodexModelLimitSnapshot>;
}

export interface CodexUsageEntry {
  user_id: number;
  name: string;
  codex_account: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  request_count: number;
}

export interface CodexUserUsageEntry {
  codex_account: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  request_count: number;
}

export async function fetchAuditUsers(): Promise<AuditUser[]> {
  try {
    const response = await fetch("/api/audit/users");
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch audit users:", error);
    return [];
  }
}

export async function fetchAuditUsage(period: string = "24h"): Promise<AuditUsageEntry[]> {
  try {
    const response = await fetch(`/api/audit/usage?period=${period}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch audit usage:", error);
    return [];
  }
}

export async function fetchAuditUserUsage(userId: number, period: string = "7d"): Promise<AuditModelUsage[]> {
  try {
    const response = await fetch(`/api/audit/usage/${userId}?period=${period}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch user usage:", error);
    return [];
  }
}

export async function fetchAuditCaptures(userId: number, limit: number = 50, offset: number = 0): Promise<AuditCaptureInfo[]> {
  try {
    const response = await fetch(`/api/audit/captures?user_id=${userId}&limit=${limit}&offset=${offset}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch audit captures:", error);
    return [];
  }
}

export async function fetchPerformance(after?: string): Promise<PerformanceResponse | null> {
  try {
    const url = after ? `/api/performance?after=${encodeURIComponent(after)}` : "/api/performance";
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch performance data:", error);
    return null;
  }
}

export async function fetchCodexAccounts(period: string = "7d"): Promise<CodexAccount[]> {
  try {
    const response = await fetch(`/api/codex/accounts?period=${period}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch codex accounts:", error);
    return [];
  }
}

export async function fetchCodexUsage(period: string = "7d"): Promise<CodexUsageEntry[]> {
  try {
    const response = await fetch(`/api/codex/usage?period=${period}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch codex usage:", error);
    return [];
  }
}

export async function fetchCodexUserUsage(userId: number, period: string = "7d"): Promise<CodexUserUsageEntry[]> {
  try {
    const response = await fetch(`/api/codex/usage/${userId}?period=${period}`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch codex user usage:", error);
    return [];
  }
}

export async function fetchActivityHistory(): Promise<ActivityLogEntry[]> {
  try {
    const response = await fetch("/api/audit/activity?limit=200");
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch activity history:", error);
    return [];
  }
}
