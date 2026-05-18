import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { get } from "svelte/store";
import { codexAccounts, enableAPIEvents } from "./api";
import { currentRoute } from "./route";

class MockEventSource {
  url: string;
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  readyState = 0;

  static instances: MockEventSource[] = [];

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  close() {
    this.readyState = 2;
  }

  simulateMessage(type: string, data: unknown) {
    if (this.onmessage) {
      this.onmessage({
        data: JSON.stringify({ type, data: JSON.stringify(data) }),
      } as MessageEvent);
    }
  }

  simulateOpen() {
    this.readyState = 1;
    if (this.onopen) {
      this.onopen(new Event("open"));
    }
  }
}

describe("codexQuota SSE event", () => {
  let mockFetch: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);

    mockFetch = vi.fn((url: string) => {
      if (url.startsWith("/api/codex/accounts")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () =>
            Promise.resolve([
              {
                name: "test-account",
                plan_type: "free",
                active_limit: "standard",
                token_valid: true,
                token_expires_at: 9999999999,
                account_id: "acct-1",
                primary: { used_percent: 10, reset_after_seconds: 300, reset_at: 0, window_minutes: 60 },
                secondary: { used_percent: 20, reset_after_seconds: 600, reset_at: 0, window_minutes: 120 },
                credits: { balance: "100", has_credits: true, unlimited: false },
                stats: null,
                model_limits: {},
              },
            ]),
        } as Response);
      }
      if (url === "/api/audit/activity?limit=200") {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve([]),
        } as Response);
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      } as Response);
    });
    vi.stubGlobal("fetch", mockFetch);
  });

  afterEach(() => {
    enableAPIEvents(false);
    vi.restoreAllMocks();
  });

  it("fetches codex accounts when on /codex route", async () => {
    currentRoute.set("/codex");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await vi.waitFor(() => expect(mockFetch).toHaveBeenCalledWith("/api/codex/accounts?period=7d"));
  });

  it("does not fetch codex accounts when on / route", async () => {
    currentRoute.set("/");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await new Promise((r) => setTimeout(r, 50));

    expect(mockFetch).not.toHaveBeenCalledWith("/api/codex/accounts");
  });

  it("does not fetch codex accounts when on /models route", async () => {
    currentRoute.set("/models");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await new Promise((r) => setTimeout(r, 50));

    expect(mockFetch).not.toHaveBeenCalledWith("/api/codex/accounts");
  });

  it("does not fetch codex accounts when on /activity route", async () => {
    currentRoute.set("/activity");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await new Promise((r) => setTimeout(r, 50));

    expect(mockFetch).not.toHaveBeenCalledWith("/api/codex/accounts");
  });

  it("updates codexAccounts store when on /codex route and accounts returned", async () => {
    currentRoute.set("/codex");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await vi.waitFor(() => {
      const accounts = get(codexAccounts);
      expect(accounts).toHaveLength(1);
      expect(accounts[0].name).toBe("test-account");
    });
  });

  it("does not update codexAccounts store when not on /codex route", async () => {
    codexAccounts.set([]);

    currentRoute.set("/logs");
    enableAPIEvents(true);

    const es = MockEventSource.instances[0];
    es.simulateOpen();
    mockFetch.mockClear();

    es.simulateMessage("codexQuota", {});

    await new Promise((r) => setTimeout(r, 50));

    const accounts = get(codexAccounts);
    expect(accounts).toHaveLength(0);
  });
});
