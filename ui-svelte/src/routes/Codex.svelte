<script lang="ts">
  import { onMount } from "svelte";
  import CodexAccounts from "../components/CodexAccounts.svelte";
  import CodexUsage from "../components/CodexUsage.svelte";
  import { codexAccounts, fetchCodexAccounts, fetchCodexUsage, type CodexUsageEntry } from "../stores/api";

  let period = $state("7d");
  let usage = $state<CodexUsageEntry[]>([]);
  let codexEnabled = $state(true);
  let accountsLoading = $state(true);
  let usageLoading = $state(true);

  async function loadAccounts() {
    accountsLoading = true;
    try {
      const accounts = await fetchCodexAccounts();
      codexAccounts.set(accounts);

      if (accounts.length === 0) {
        try {
          const response = await fetch("/api/codex/accounts");
          codexEnabled = response.status !== 404;
        } catch {
          codexEnabled = false;
        }
      } else {
        codexEnabled = true;
      }
    } finally {
      accountsLoading = false;
    }
  }

  async function loadUsage(nextPeriod: string) {
    usageLoading = true;
    try {
      usage = await fetchCodexUsage(nextPeriod);
    } finally {
      usageLoading = false;
    }
  }

  async function changePeriod(newPeriod: string) {
    period = newPeriod;
    await loadUsage(period);
  }

  async function loadData() {
    accountsLoading = true;
    usageLoading = true;
    await Promise.all([loadAccounts(), loadUsage(period)]);
  }

  onMount(() => {
    loadData();
  });
</script>

<div class="p-2">
  <div class="mb-6">
    <h1 class="text-2xl font-semibold pb-2">Codex</h1>
    <p class="text-sm text-gray-500 dark:text-gray-400">
      Monitor codex account quota, token validity, and per-user usage.
    </p>
  </div>

  <CodexAccounts accounts={$codexAccounts} {codexEnabled} loading={accountsLoading} />

  {#if codexEnabled}
    <CodexUsage {usage} {period} loading={usageLoading} onPeriodChange={changePeriod} />
  {/if}
</div>
