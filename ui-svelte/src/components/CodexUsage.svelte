<script lang="ts">
  import { fetchCodexUserUsage, type CodexUsageEntry, type CodexUserUsageEntry } from "../stores/api";

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

  function formatNumber(n: number): string {
    return n.toLocaleString();
  }

  function rowKey(entry: CodexUsageEntry): string {
    return `${entry.user_id}:${entry.codex_account}`;
  }

  function isSelected(entry: CodexUsageEntry): boolean {
    return selectedUserId === entry.user_id && selectedAccount === entry.codex_account;
  }

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
        <th class="px-6 py-3">User</th>
        <th class="px-6 py-3">Account</th>
        <th class="px-6 py-3">Input Tokens</th>
        <th class="px-6 py-3">Output Tokens</th>
        <th class="px-6 py-3">Cached Tokens</th>
        <th class="px-6 py-3">Requests</th>
      </tr>
    </thead>
    <tbody class="divide-y">
      {#if loading}
        <tr>
          <td colspan={6} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            Loading codex usage data...
          </td>
        </tr>
      {:else if usage.length === 0}
        <tr>
          <td colspan={6} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            No codex usage data for this period
          </td>
        </tr>
      {:else}
        {#each usage as entry (rowKey(entry))}
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
            <td class="px-6 py-4">{formatNumber(entry.request_count)}</td>
          </tr>
        {/each}
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
            <th class="px-6 py-3">Model</th>
            <th class="px-6 py-3">Input Tokens</th>
            <th class="px-6 py-3">Output Tokens</th>
            <th class="px-6 py-3">Cached Tokens</th>
            <th class="px-6 py-3">Requests</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          {#each modelUsage as model (model.model)}
            <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
              <td class="px-6 py-4">{model.model}</td>
              <td class="px-6 py-4">{formatNumber(model.input_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.output_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.cached_tokens)}</td>
              <td class="px-6 py-4">{formatNumber(model.request_count)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
{/if}
