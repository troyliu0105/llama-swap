<script lang="ts">
  import { onMount } from "svelte";
  import {
    fetchAuditUsers,
    fetchAuditUsage,
    fetchAuditUserUsage,
    fetchAuditCaptures,
    type AuditUser,
    type AuditUsageEntry,
    type AuditModelUsage,
    type AuditCaptureInfo,
  } from "../stores/api";
  import { sumTokenTotals, sumTokenTotalsWithRequests } from "../lib/tokenTotals";

  let period = $state("24h");
  let users = $state<AuditUser[]>([]);
  let usage = $state<AuditUsageEntry[]>([]);
  let auditEnabled = $state(true);
  let loading = $state(true);

  // User detail panel
  let selectedUserId = $state<number | null>(null);
  let selectedUserName = $state("");
  let modelUsage = $state<AuditModelUsage[]>([]);
  let captures = $state<AuditCaptureInfo[]>([]);
  let capturesOffset = $state(0);
  const capturesLimit = 20;
  let capturesHasMore = $state(false);

  function maskApiKey(key: string): string {
    if (key.length <= 8) return key;
    return key.slice(0, 4) + "..." + key.slice(-4);
  }

  function formatNumber(n: number): string {
    return n.toLocaleString();
  }

  function formatTimestamp(ts: string): string {
    if (!ts) return "-";
    const date = new Date(ts);
    return date.toLocaleString();
  }

  async function loadData() {
    loading = true;
    try {
      const [usersResult, usageResult] = await Promise.all([
        fetchAuditUsers(),
        fetchAuditUsage(period),
      ]);

      if (usersResult.length === 0 && usageResult.length === 0) {
        // Check if audit is disabled by probing the users endpoint directly
        try {
          const resp = await fetch("/api/audit/users");
          if (resp.status === 404) {
            auditEnabled = false;
          }
        } catch {
          auditEnabled = false;
        }
      }

      users = usersResult;
      usage = usageResult;
    } catch {
      auditEnabled = false;
    } finally {
      loading = false;
    }
  }

  async function changePeriod(newPeriod: string) {
    period = newPeriod;
    usage = await fetchAuditUsage(period);
    if (selectedUserId !== null) {
      modelUsage = await fetchAuditUserUsage(selectedUserId, period);
    }
  }

  async function selectUser(user: AuditUser) {
    selectedUserId = user.id;
    selectedUserName = user.name;
    capturesOffset = 0;
    captures = [];
    modelUsage = await fetchAuditUserUsage(user.id, period);
    captures = await fetchAuditCaptures(user.id, capturesLimit, 0);
    capturesHasMore = captures.length === capturesLimit;
  }

  async function loadMoreCaptures() {
    if (selectedUserId === null) return;
    capturesOffset += capturesLimit;
    const more = await fetchAuditCaptures(selectedUserId, capturesLimit, capturesOffset);
    captures = [...captures, ...more];
    capturesHasMore = more.length === capturesLimit;
  }

  function closeUserDetail() {
    selectedUserId = null;
    selectedUserName = "";
    modelUsage = [];
    captures = [];
    capturesOffset = 0;
    capturesHasMore = false;
  }

  let usageTotals = $derived(sumTokenTotals(usage));
  let usageTotalRequests = $derived(users.reduce((s, u) => s + u.total_requests, 0));
  let modelTotals = $derived(sumTokenTotalsWithRequests(modelUsage));

  onMount(() => {
    loadData();
  });
</script>

<div class="p-2">
  {#if loading}
    <div class="flex items-center justify-center py-16 text-sm text-gray-500 dark:text-gray-400">
      Loading audit data...
    </div>
  {:else if !auditEnabled}
    <div class="card p-8 text-center">
      <h2 class="text-lg font-semibold mb-2">Audit Not Enabled</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400">
        Audit logging is not configured. Enable it in your llama-swap configuration to track per-user token usage and request captures.
      </p>
    </div>
  {:else}
    <div class="mt-4 mb-4 flex items-center gap-3">
      <span class="text-sm font-medium text-gray-700 dark:text-gray-300">Period:</span>
      <div class="flex rounded border border-gray-200 dark:border-white/10 overflow-hidden">
        {#each ["24h", "7d", "30d"] as p}
          <button
            class="px-3 py-1 text-sm transition-colors {period === p
              ? 'bg-gray-800 text-white dark:bg-white dark:text-gray-800'
              : 'bg-surface text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'}"
            onclick={() => changePeriod(p)}
          >
            {p}
          </button>
        {/each}
      </div>
    </div>

    <div class="card overflow-auto min-h-[20rem] mb-6">
      <table class="min-w-full divide-y">
        <thead class="border-gray-200 dark:border-white/10">
          <tr class="text-left text-xs uppercase tracking-wider">
            <th class="px-6 py-3">Name</th>
            <th class="px-6 py-3">API Key</th>
            <th class="px-6 py-3">Input Tokens</th>
            <th class="px-6 py-3">Output Tokens</th>
            <th class="px-6 py-3">Cached Tokens</th>
            <th class="px-6 py-3">Total Requests</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          {#if usage.length === 0}
            <tr>
              <td colspan={6} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
                No usage data for this period
              </td>
            </tr>
          {:else}
            {#each usage as entry (entry.user_id)}
              {@const user = users.find((u) => u.id === entry.user_id)}
              <tr
                class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
                class:bg-gray-100={selectedUserId === entry.user_id}
                class:dark:bg-gray-700={selectedUserId === entry.user_id}
                onclick={() => user && selectUser(user)}
              >
                <td class="px-6 py-4">{entry.name || "-"}</td>
                <td class="px-6 py-4 font-mono text-xs">{user ? maskApiKey(user.api_key) : "-"}</td>
                <td class="px-6 py-4">{formatNumber(entry.input_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(entry.output_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(entry.cached_tokens)}</td>
                <td class="px-6 py-4">{user ? formatNumber(user.total_requests) : "-"}</td>
              </tr>
            {/each}
            <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
              <td class="px-6 py-4">Total</td>
              <td class="px-6 py-4"></td>
              <td class="px-6 py-4">{formatNumber(usageTotals.input_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(usageTotals.output_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(usageTotals.cached_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(usageTotalRequests)}</td>
            </tr>
            <tr class="whitespace-nowrap text-sm text-gray-500 dark:text-gray-400">
              <td class="px-6 py-2" colspan="2"></td>
              <td class="px-6 py-2 text-xs">Input + Output: {formatNumber(usageTotals.input_plus_output)}</td>
              <td class="px-6 py-2" colspan="3"></td>
            </tr>
          {/if}
        </tbody>
      </table>
    </div>

    {#if selectedUserId !== null}
      <div class="card p-4 mb-6">
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-lg font-semibold">
            {selectedUserName} - Model Usage ({period})
          </h2>
          <button
            class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            onclick={closeUserDetail}
          >
            Close
          </button>
        </div>

        {#if modelUsage.length === 0}
          <p class="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
            No model usage data for this user in the selected period.
          </p>
        {:else}
          <table class="min-w-full divide-y">
            <thead class="border-gray-200 dark:border-white/10">
              <tr class="text-left text-xs uppercase tracking-wider">
                <th class="px-6 py-3">Model</th>
                <th class="px-6 py-3">Input Tokens</th>
                <th class="px-6 py-3">Output Tokens</th>
                <th class="px-6 py-3">Cached Tokens</th>
                <th class="px-6 py-3">Requests</th>
              </tr>
            </thead>
            <tbody class="divide-y">
              {#each modelUsage as mu (mu.model)}
                <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
                  <td class="px-6 py-4">{mu.model}</td>
                  <td class="px-6 py-4">{formatNumber(mu.input_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.output_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.cached_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.request_count)}</td>
                </tr>
              {/each}
              <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
                <td class="px-6 py-4">Total</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.input_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.output_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.cached_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.request_count)}</td>
              </tr>
              <tr class="whitespace-nowrap text-sm text-gray-500 dark:text-gray-400">
                <td class="px-6 py-2 text-xs">Input + Output: {formatNumber(modelTotals.input_plus_output)}</td>
                <td class="px-6 py-2" colspan="4"></td>
              </tr>
            </tbody>
          </table>
        {/if}
      </div>

      <div class="card p-4">
        <h2 class="text-lg font-semibold mb-4">
          {selectedUserName} - Captures
        </h2>

        {#if captures.length === 0}
          <p class="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
            No captures found for this user.
          </p>
        {:else}
          <table class="min-w-full divide-y">
            <thead class="border-gray-200 dark:border-white/10">
              <tr class="text-left text-xs uppercase tracking-wider">
                <th class="px-6 py-3">ID</th>
                <th class="px-6 py-3">Session</th>
                <th class="px-6 py-3">Model</th>
                <th class="px-6 py-3">Path</th>
                <th class="px-6 py-3">Seq</th>
                <th class="px-6 py-3">Time</th>
              </tr>
            </thead>
            <tbody class="divide-y">
              {#each captures as cap (cap.id)}
                <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
                  <td class="px-6 py-4">{cap.id}</td>
                  <td class="px-6 py-4">{cap.session_id}</td>
                  <td class="px-6 py-4">{cap.model}</td>
                  <td class="px-6 py-4">{cap.req_path}</td>
                  <td class="px-6 py-4">{cap.seq_num}</td>
                  <td class="px-6 py-4">{formatTimestamp(cap.created_at)}</td>
                </tr>
              {/each}
            </tbody>
          </table>

          {#if capturesHasMore}
            <div class="flex justify-center py-4">
              <button class="btn" onclick={loadMoreCaptures}>
                Load More
              </button>
            </div>
          {/if}
        {/if}
      </div>
    {/if}
  {/if}
</div>
