<script lang="ts">
  import { onMount } from "svelte";
  import {
    fetchAuditUsers,
    fetchAuditUsage,
    fetchAuditUserUsage,
    fetchAuditCaptures,
    fetchAuditModelUsage,
    type AuditUser,
    type AuditUsageEntry,
    type AuditModelUsage,
    type AuditCaptureInfo,
  } from "../stores/api";
  import { sumTokenTotals, sumTokenTotalsWithRequests } from "../lib/tokenTotals";
  import { type SortState, toggleSort, sortBy, sortIndicator } from "../lib/tableSort";

  type Tab = "users" | "models";

  let activeTab = $state<Tab>("users");
  let period = $state("24h");
  let users = $state<AuditUser[]>([]);
  let usage = $state<AuditUsageEntry[]>([]);
  let auditEnabled = $state(true);
  let loading = $state(true);

  // Model usage tab
  let modelUsage = $state<AuditModelUsage[]>([]);

  // User detail panel
  let selectedUserId = $state<number | null>(null);
  let selectedUserName = $state("");
  let userModelUsage = $state<AuditModelUsage[]>([]);
  let captures = $state<AuditCaptureInfo[]>([]);
  let capturesOffset = $state(0);
  const capturesLimit = 20;
  let capturesHasMore = $state(false);

  // Sort state for each table
  type UsageSortKey = "name" | "api_key" | "input_tokens" | "output_tokens" | "cached_tokens" | "all" | "total_requests";
  type ModelSortKey = "model" | "input_tokens" | "output_tokens" | "cached_tokens" | "all" | "request_count";

  let usageSort = $state<SortState<UsageSortKey>>({ key: "name", dir: "desc" });
  let userModelSort = $state<SortState<ModelSortKey>>({ key: "model", dir: "desc" });
  let modelSort = $state<SortState<ModelSortKey>>({ key: "model", dir: "desc" });

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

  function usageAccessor(entry: AuditUsageEntry, key: string): string | number {
    const user = users.find((u) => u.id === entry.user_id);
    switch (key) {
      case "name": return entry.name || "";
      case "api_key": return user ? user.api_key : "";
      case "input_tokens": return entry.input_tokens;
      case "output_tokens": return entry.output_tokens;
      case "cached_tokens": return entry.cached_tokens;
      case "all": return entry.input_tokens + entry.output_tokens;
      case "total_requests": return user ? user.total_requests : 0;
      default: return 0;
    }
  }

  function modelUsageAccessor(mu: AuditModelUsage, key: string): string | number {
    switch (key) {
      case "model": return mu.model;
      case "input_tokens": return mu.input_tokens;
      case "output_tokens": return mu.output_tokens;
      case "cached_tokens": return mu.cached_tokens;
      case "all": return mu.input_tokens + mu.output_tokens;
      case "request_count": return mu.request_count;
      default: return 0;
    }
  }

  let sortedUsage = $derived(sortBy(usage, usageSort, usageAccessor));
  let sortedUserModelUsage = $derived(sortBy(userModelUsage, userModelSort, modelUsageAccessor));
  let sortedModelUsage = $derived(sortBy(modelUsage, modelSort, modelUsageAccessor));

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

  async function loadModelUsageData() {
    modelUsage = await fetchAuditModelUsage(period);
  }

  async function changePeriod(newPeriod: string) {
    period = newPeriod;
    usage = await fetchAuditUsage(period);
    if (activeTab === "models") {
      await loadModelUsageData();
    }
    if (selectedUserId !== null) {
      userModelUsage = await fetchAuditUserUsage(selectedUserId, period);
    }
  }

  async function selectUser(user: AuditUser) {
    selectedUserId = user.id;
    selectedUserName = user.name;
    capturesOffset = 0;
    captures = [];
    userModelUsage = await fetchAuditUserUsage(user.id, period);
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
    userModelUsage = [];
    captures = [];
    capturesOffset = 0;
    capturesHasMore = false;
  }

  function switchTab(tab: Tab) {
    activeTab = tab;
    if (tab === "models" && modelUsage.length === 0) {
      loadModelUsageData();
    }
  }

  let usageTotals = $derived(sumTokenTotals(usage));
  let usageTotalRequests = $derived(users.reduce((s, u) => s + u.total_requests, 0));
  let userModelTotals = $derived(sumTokenTotalsWithRequests(userModelUsage));
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
      <!-- Tab buttons -->
      <div class="flex rounded border border-gray-200 dark:border-white/10 overflow-hidden">
        <button
          class="px-3 py-1 text-sm transition-colors {activeTab === 'users'
            ? 'bg-gray-800 text-white dark:bg-white dark:text-gray-800'
            : 'bg-surface text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'}"
          onclick={() => switchTab("users")}
        >
          Users
        </button>
        <button
          class="px-3 py-1 text-sm transition-colors {activeTab === 'models'
            ? 'bg-gray-800 text-white dark:bg-white dark:text-gray-800'
            : 'bg-surface text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'}"
          onclick={() => switchTab("models")}
        >
          Model Usage
        </button>
      </div>

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

    {#if activeTab === "users"}
      <div class="card overflow-auto min-h-[20rem] mb-6">
        <table class="min-w-full divide-y">
          <thead class="border-gray-200 dark:border-white/10">
            <tr class="text-left text-xs uppercase tracking-wider">
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "name")}>Name{sortIndicator(usageSort, "name")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "api_key")}>API Key{sortIndicator(usageSort, "api_key")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "input_tokens")}>Input Tokens{sortIndicator(usageSort, "input_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "output_tokens")}>Output Tokens{sortIndicator(usageSort, "output_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "cached_tokens")}>Cached Tokens{sortIndicator(usageSort, "cached_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "all")}>All{sortIndicator(usageSort, "all")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "total_requests")}>Total Requests{sortIndicator(usageSort, "total_requests")}</button></th>
            </tr>
          </thead>
          <tbody class="divide-y">
            {#if usage.length === 0}
              <tr>
                <td colspan={7} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
                  No usage data for this period
                </td>
              </tr>
            {:else}
              {#each sortedUsage as entry (entry.user_id)}
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
                  <td class="px-6 py-4">{formatNumber(entry.input_tokens + entry.output_tokens)}</td>
                  <td class="px-6 py-4">{user ? formatNumber(user.total_requests) : "-"}</td>
                </tr>
              {/each}
              <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
                <td class="px-6 py-4">Total</td>
                <td class="px-6 py-4"></td>
                <td class="px-6 py-4">{formatNumber(usageTotals.input_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(usageTotals.output_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(usageTotals.cached_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(usageTotals.input_plus_output)}</td>
                <td class="px-6 py-4">{formatNumber(usageTotalRequests)}</td>
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

          {#if userModelUsage.length === 0}
            <p class="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
              No model usage data for this user in the selected period.
            </p>
          {:else}
            <table class="min-w-full divide-y">
              <thead class="border-gray-200 dark:border-white/10">
                <tr class="text-left text-xs uppercase tracking-wider">
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "model")}>Model{sortIndicator(userModelSort, "model")}</button></th>
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "input_tokens")}>Input Tokens{sortIndicator(userModelSort, "input_tokens")}</button></th>
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "output_tokens")}>Output Tokens{sortIndicator(userModelSort, "output_tokens")}</button></th>
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "cached_tokens")}>Cached Tokens{sortIndicator(userModelSort, "cached_tokens")}</button></th>
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "all")}>All{sortIndicator(userModelSort, "all")}</button></th>
                  <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => userModelSort = toggleSort(userModelSort, "request_count")}>Requests{sortIndicator(userModelSort, "request_count")}</button></th>
                </tr>
              </thead>
              <tbody class="divide-y">
                {#each sortedUserModelUsage as mu (mu.model)}
                  <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
                    <td class="px-6 py-4">{mu.model}</td>
                    <td class="px-6 py-4">{formatNumber(mu.input_tokens)}</td>
                    <td class="px-6 py-4">{formatNumber(mu.output_tokens)}</td>
                    <td class="px-6 py-4">{formatNumber(mu.cached_tokens)}</td>
                    <td class="px-6 py-4">{formatNumber(mu.input_tokens + mu.output_tokens)}</td>
                    <td class="px-6 py-4">{formatNumber(mu.request_count)}</td>
                  </tr>
                {/each}
                <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
                  <td class="px-6 py-4">Total</td>
                  <td class="px-6 py-4">{formatNumber(userModelTotals.input_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(userModelTotals.output_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(userModelTotals.cached_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(userModelTotals.input_plus_output)}</td>
                  <td class="px-6 py-4">{formatNumber(userModelTotals.request_count)}</td>
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
    {:else if activeTab === "models"}
      <div class="card overflow-auto min-h-[20rem]">
        <table class="min-w-full divide-y">
          <thead class="border-gray-200 dark:border-white/10">
            <tr class="text-left text-xs uppercase tracking-wider">
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "model")}>Model{sortIndicator(modelSort, "model")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "input_tokens")}>Input Tokens{sortIndicator(modelSort, "input_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "output_tokens")}>Output Tokens{sortIndicator(modelSort, "output_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "cached_tokens")}>Cached Tokens{sortIndicator(modelSort, "cached_tokens")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "all")}>All{sortIndicator(modelSort, "all")}</button></th>
              <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => modelSort = toggleSort(modelSort, "request_count")}>Requests{sortIndicator(modelSort, "request_count")}</button></th>
            </tr>
          </thead>
          <tbody class="divide-y">
            {#if modelUsage.length === 0}
              <tr>
                <td colspan={6} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
                  No model usage data for this period
                </td>
              </tr>
            {:else}
              {#each sortedModelUsage as mu (mu.model)}
                <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
                  <td class="px-6 py-4">{mu.model}</td>
                  <td class="px-6 py-4">{formatNumber(mu.input_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.output_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.cached_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.input_tokens + mu.output_tokens)}</td>
                  <td class="px-6 py-4">{formatNumber(mu.request_count)}</td>
                </tr>
              {/each}
              <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
                <td class="px-6 py-4">Total</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.input_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.output_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.cached_tokens)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.input_plus_output)}</td>
                <td class="px-6 py-4">{formatNumber(modelTotals.request_count)}</td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>
