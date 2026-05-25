<script lang="ts">
  import { fetchCodexUserUsage, type CodexUsageEntry, type CodexUserUsageEntry } from "../stores/api";
  import { sumTokenTotalsWithRequests } from "../lib/tokenTotals";
  import { type SortState, toggleSort, sortBy, sortIndicator, loadSortState, saveSortState } from "../lib/tableSort";

  interface Props {
    usage: CodexUsageEntry[];
    period: string;
    loading: boolean;
    onPeriodChange: (period: string) => Promise<void>;
  }

  let { usage, period, loading, onPeriodChange }: Props = $props();
  let selectedUserId = $state<number | null>(null);
  let selectedAccount = $state("");
  let selectedName = $state("");
  let modelUsage = $state<CodexUserUsageEntry[]>([]);
  let detailLoading = $state(false);

  type UsageSortKey = "name" | "codex_account" | "input_tokens" | "output_tokens" | "cached_tokens" | "all" | "request_count";
  type ModelSortKey = "model" | "input_tokens" | "output_tokens" | "cached_tokens" | "all" | "request_count";

  let usageSort = $state<SortState<UsageSortKey>>(loadSortState("codex-usage", "name"));
  let modelSort = $state<SortState<ModelSortKey>>(loadSortState("codex-model", "model"));

  $effect(() => { saveSortState("codex-usage", usageSort); });
  $effect(() => { saveSortState("codex-model", modelSort); });

  function formatNumber(n: number): string {
    return n.toLocaleString();
  }

  function rowKey(entry: CodexUsageEntry): string {
    return `${entry.user_id}:${entry.codex_account}`;
  }

  function isSelected(entry: CodexUsageEntry): boolean {
    return selectedUserId === entry.user_id && selectedAccount === entry.codex_account;
  }

  function usageAccessor(entry: CodexUsageEntry, key: string): string | number {
    switch (key) {
      case "name": return entry.name || "";
      case "codex_account": return entry.codex_account || "";
      case "input_tokens": return entry.input_tokens;
      case "output_tokens": return entry.output_tokens;
      case "cached_tokens": return entry.cached_tokens;
      case "all": return entry.input_tokens + entry.output_tokens;
      case "request_count": return entry.request_count;
      default: return 0;
    }
  }

  function modelAccessor(m: CodexUserUsageEntry, key: string): string | number {
    switch (key) {
      case "model": return m.model;
      case "input_tokens": return m.input_tokens;
      case "output_tokens": return m.output_tokens;
      case "cached_tokens": return m.cached_tokens;
      case "all": return m.input_tokens + m.output_tokens;
      case "request_count": return m.request_count;
      default: return 0;
    }
  }

  let sortedUsage = $derived(sortBy(usage, usageSort, usageAccessor));
  let sortedModelUsage = $derived(sortBy(modelUsage, modelSort, modelAccessor));

  async function selectUsage(entry: CodexUsageEntry) {
    if (isSelected(entry)) {
      closeDetail();
      return;
    }

    selectedUserId = entry.user_id;
    selectedAccount = entry.codex_account;
    selectedName = entry.name;
    detailLoading = true;
    try {
      const rows = await fetchCodexUserUsage(entry.user_id, period);
      modelUsage = rows.filter((row) => row.codex_account === entry.codex_account);
    } finally {
      detailLoading = false;
    }
  }

  async function changePeriod(newPeriod: string) {
    await onPeriodChange(newPeriod);
    if (selectedUserId !== null) {
      detailLoading = true;
      try {
        const rows = await fetchCodexUserUsage(selectedUserId, newPeriod);
        modelUsage = rows.filter((row) => row.codex_account === selectedAccount);
      } finally {
        detailLoading = false;
      }
    }
  }

  function closeDetail() {
    selectedUserId = null;
    selectedAccount = "";
    selectedName = "";
    modelUsage = [];
  }

  let usageTotals = $derived(sumTokenTotalsWithRequests(usage));
  let modelTotals = $derived(sumTokenTotalsWithRequests(modelUsage));
</script>

<div class="mt-6 mb-4 flex items-center gap-3">
  <span class="text-sm font-medium text-gray-700 dark:text-gray-300">Period:</span>
  <div class="flex rounded border border-gray-200 dark:border-white/10 overflow-hidden">
    {#each ["5h", "24h", "7d", "30d"] as p}
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
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "name")}>User{sortIndicator(usageSort, "name")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "codex_account")}>Account{sortIndicator(usageSort, "codex_account")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "input_tokens")}>Input Tokens{sortIndicator(usageSort, "input_tokens")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "output_tokens")}>Output Tokens{sortIndicator(usageSort, "output_tokens")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "cached_tokens")}>Cached Tokens{sortIndicator(usageSort, "cached_tokens")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "all")}>All{sortIndicator(usageSort, "all")}</button></th>
        <th class="px-6 py-3"><button class="cursor-pointer hover:text-gray-900 dark:hover:text-white" onclick={() => usageSort = toggleSort(usageSort, "request_count")}>Requests{sortIndicator(usageSort, "request_count")}</button></th>
      </tr>
    </thead>
    <tbody class="divide-y">
      {#if loading}
        <tr>
          <td colspan={7} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            Loading codex usage data...
          </td>
        </tr>
      {:else if usage.length === 0}
        <tr>
          <td colspan={7} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            No codex usage data for this period
          </td>
        </tr>
      {:else}
        {#each sortedUsage as entry (rowKey(entry))}
          <tr
            class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
            class:bg-gray-100={isSelected(entry)}
            class:dark:bg-gray-700={isSelected(entry)}
            onclick={() => selectUsage(entry)}
          >
            <td class="px-6 py-4">{entry.name || "-"}</td>
            <td class="px-6 py-4">{entry.codex_account || "-"}</td>
            <td class="px-6 py-4">{formatNumber(entry.input_tokens)}</td>
            <td class="px-6 py-4">{formatNumber(entry.output_tokens)}</td>
            <td class="px-6 py-4">{formatNumber(entry.cached_tokens)}</td>
            <td class="px-6 py-4">{formatNumber(entry.input_tokens + entry.output_tokens)}</td>
            <td class="px-6 py-4">{formatNumber(entry.request_count)}</td>
          </tr>
        {/each}
        <tr class="whitespace-nowrap text-sm font-semibold border-t-2 border-gray-300 dark:border-gray-500">
          <td class="px-6 py-4">Total</td>
          <td class="px-6 py-4"></td>
          <td class="px-6 py-4">{formatNumber(usageTotals.input_tokens)}</td>
          <td class="px-6 py-4">{formatNumber(usageTotals.output_tokens)}</td>
          <td class="px-6 py-4">{formatNumber(usageTotals.cached_tokens)}</td>
          <td class="px-6 py-4">{formatNumber(usageTotals.input_plus_output)}</td>
          <td class="px-6 py-4">{formatNumber(usageTotals.request_count)}</td>
        </tr>
      {/if}
    </tbody>
  </table>
</div>

{#if selectedUserId !== null}
  <div class="card p-4 mb-6">
    <div class="flex items-center justify-between mb-4">
      <h2 class="text-lg font-semibold">
        {selectedName || "User " + selectedUserId} - {selectedAccount} Model Usage ({period})
      </h2>
      <button
        class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
        onclick={closeDetail}
      >
        Close
      </button>
    </div>

    {#if detailLoading}
      <p class="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
        Loading model usage data...
      </p>
    {:else if modelUsage.length === 0}
      <p class="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
        No model usage data for this user and account in the selected period.
      </p>
    {:else}
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
          {#each sortedModelUsage as model (model.model)}
            <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
              <td class="px-6 py-4">{model.model}</td>
              <td class="px-6 py-4">{formatNumber(model.input_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.output_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.cached_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.input_tokens + model.output_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.request_count)}</td>
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
        </tbody>
      </table>
    {/if}
  </div>
{/if}
