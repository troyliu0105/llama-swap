<script lang="ts">
  import type { CodexAccount, CodexModelLimitSnapshot, CodexWindowSnapshot } from "../stores/api";

  interface Props {
    accounts: CodexAccount[];
    codexEnabled: boolean;
    loading: boolean;
  }

  let { accounts, codexEnabled, loading }: Props = $props();

  function formatNumber(n: number): string {
    return n.toLocaleString();
  }

  function formatResetTime(seconds: number): string {
    if (seconds <= 0) return "now";
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m`;
  }

  function formatExpiry(expiresAt: number): string {
    const now = Math.floor(Date.now() / 1000);
    const diff = expiresAt - now;
    if (diff <= 0) return "expired";
    return formatResetTime(diff);
  }

  function progressBarColor(pct: number): string {
    if (pct >= 80) return "bg-red-500";
    if (pct >= 50) return "bg-yellow-500";
    return "bg-green-500";
  }

  function clampPercent(pct: number): number {
    return Math.max(0, Math.min(100, pct));
  }

  function modelLimitEntries(account: CodexAccount): [string, CodexModelLimitSnapshot][] {
    return Object.entries(account.model_limits ?? {}).sort(([a], [b]) => a.localeCompare(b));
  }

  function labelForWindow(window: CodexWindowSnapshot): string {
    return `${window.used_percent.toFixed(1)}% used, resets in ${formatResetTime(window.reset_after_seconds)}`;
  }
</script>

{#if loading}
  <div class="flex items-center justify-center py-12 text-sm text-gray-500 dark:text-gray-400">
    Loading codex account data...
  </div>
{:else if !codexEnabled}
  <div class="card p-8 text-center">
    <h2 class="text-lg font-semibold mb-2">Codex Not Configured</h2>
    <p class="text-sm text-gray-500 dark:text-gray-400">
      Codex account endpoints are not available. Configure codex support in llama-swap to view account quota and usage.
    </p>
  </div>
{:else if accounts.length === 0}
  <div class="card p-8 text-center">
    <h2 class="text-lg font-semibold mb-2">Waiting for Codex Data</h2>
    <p class="text-sm text-gray-500 dark:text-gray-400">
      No codex accounts have reported quota data yet. The dashboard will update when account status is available.
    </p>
  </div>
{:else}
  <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
    {#each accounts as account (account.name)}
      <section class="card p-4">
        <div class="flex items-start justify-between gap-3 mb-4">
          <div>
            <h2 class="text-lg font-semibold pb-1">{account.name}</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400 font-mono">{account.account_id || "-"}</p>
          </div>
          <span class="status bg-primary/10 text-primary uppercase tracking-wide">{account.plan_type || "unknown"}</span>
        </div>

        <div class="space-y-4">
          <div>
            <div class="flex justify-between text-xs mb-1">
              <span class="font-medium">Primary window</span>
              <span class="text-gray-500 dark:text-gray-400">{labelForWindow(account.primary)}</span>
            </div>
            <div class="h-2 rounded-full bg-gray-200 dark:bg-gray-700 overflow-hidden">
              <div
                class="h-full rounded-full {progressBarColor(account.primary.used_percent)}"
                style:width={`${clampPercent(account.primary.used_percent)}%`}
              ></div>
            </div>
          </div>

          <div>
            <div class="flex justify-between text-xs mb-1">
              <span class="font-medium">Secondary window</span>
              <span class="text-gray-500 dark:text-gray-400">{labelForWindow(account.secondary)}</span>
            </div>
            <div class="h-2 rounded-full bg-gray-200 dark:bg-gray-700 overflow-hidden">
              <div
                class="h-full rounded-full {progressBarColor(account.secondary.used_percent)}"
                style:width={`${clampPercent(account.secondary.used_percent)}%`}
              ></div>
            </div>
          </div>
        </div>

        <dl class="grid grid-cols-2 gap-3 mt-4 text-sm">
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Active Limit</dt>
            <dd>{account.active_limit || "-"}</dd>
          </div>
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Token</dt>
            <dd class={account.token_valid ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"}>
              {account.token_valid ? `valid, ${formatExpiry(account.token_expires_at)}` : "invalid"}
            </dd>
          </div>
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Credits</dt>
            <dd>{account.credits.unlimited ? "unlimited" : account.credits.balance || "-"}</dd>
          </div>
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Requests</dt>
            <dd>{account.stats ? formatNumber(account.stats.totalRequests) : "-"}</dd>
          </div>
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Cached Tokens</dt>
            <dd>{account.stats ? formatNumber(account.stats.cachedTokens) : "-"}</dd>
          </div>
          <div>
            <dt class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Cache Rate</dt>
            <dd>{account.stats ? `${account.stats.cacheHitRate.toFixed(1)}%` : "-"}</dd>
          </div>
        </dl>

        {#if modelLimitEntries(account).length > 0}
          <div class="mt-4 pt-4 border-t border-gray-200 dark:border-white/10">
            <h3 class="text-sm font-semibold pb-2">Per-model limits</h3>
            <div class="space-y-3">
              {#each modelLimitEntries(account) as [model, limit] (model)}
                <div class="rounded border border-gray-200 dark:border-white/10 p-3">
                  <div class="flex justify-between gap-3 text-xs mb-2">
                    <span class="font-medium truncate">{model}</span>
                    <span class="text-gray-500 dark:text-gray-400">{limit.limit_name}</span>
                  </div>
                  <div class="space-y-2">
                    <div>
                      <div class="flex justify-between text-xs mb-1">
                        <span>Primary</span>
                        <span>{limit.primary_used_percent.toFixed(1)}%</span>
                      </div>
                      <div class="h-1.5 rounded-full bg-gray-200 dark:bg-gray-700 overflow-hidden">
                        <div
                          class="h-full rounded-full {progressBarColor(limit.primary_used_percent)}"
                          style:width={`${clampPercent(limit.primary_used_percent)}%`}
                        ></div>
                      </div>
                    </div>
                    <div>
                      <div class="flex justify-between text-xs mb-1">
                        <span>Secondary</span>
                        <span>{limit.secondary_used_percent.toFixed(1)}%</span>
                      </div>
                      <div class="h-1.5 rounded-full bg-gray-200 dark:bg-gray-700 overflow-hidden">
                        <div
                          class="h-full rounded-full {progressBarColor(limit.secondary_used_percent)}"
                          style:width={`${clampPercent(limit.secondary_used_percent)}%`}
                        ></div>
                      </div>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </section>
    {/each}
  </div>
{/if}
