import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  fetchAuditUsers,
  fetchAuditUsage,
  fetchAuditUserUsage,
  fetchAuditCaptures,
} from "./api";

function mockFetchResponse(data: unknown, ok = true, status = 200) {
  return vi.fn(() =>
    Promise.resolve({
      ok,
      status,
      json: () => Promise.resolve(data),
    } as Response),
  );
}

describe("fetchAuditUsers", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("calls /api/audit/users and returns parsed data", async () => {
    const users = [
      {
        id: 1,
        api_key: "sk-12345...",
        name: "Alice",
        created_at: "2025-01-01T00:00:00Z",
        total_requests: 42,
        last_request_at: "2025-06-01T00:00:00Z",
      },
    ];
    globalThis.fetch = mockFetchResponse(users);

    const result = await fetchAuditUsers();
    expect(result).toEqual(users);
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/audit/users");
  });

  it("returns empty array on HTTP error", async () => {
    globalThis.fetch = mockFetchResponse(null, false, 500);
    const result = await fetchAuditUsers();
    expect(result).toEqual([]);
  });

  it("returns empty array on network error", async () => {
    globalThis.fetch = vi.fn(() => Promise.reject(new Error("network")));
    const result = await fetchAuditUsers();
    expect(result).toEqual([]);
  });
});

describe("fetchAuditUsage", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("calls /api/audit/usage with default period 24h", async () => {
    const usage = [{ user_id: 1, name: "Alice", input_tokens: 100, output_tokens: 200, cached_tokens: 50 }];
    globalThis.fetch = mockFetchResponse(usage);

    const result = await fetchAuditUsage();
    expect(result).toEqual(usage);
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/audit/usage?period=24h");
  });

  it("calls /api/audit/usage with custom period", async () => {
    globalThis.fetch = mockFetchResponse([]);

    await fetchAuditUsage("7d");
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/audit/usage?period=7d");
  });

  it("returns empty array on error", async () => {
    globalThis.fetch = mockFetchResponse(null, false, 500);
    const result = await fetchAuditUsage("30d");
    expect(result).toEqual([]);
  });
});

describe("fetchAuditUserUsage", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("calls /api/audit/usage/:userId with period", async () => {
    const modelUsage = [
      { model: "gpt-4o", input_tokens: 50, output_tokens: 100, cached_tokens: 0, request_count: 5 },
    ];
    globalThis.fetch = mockFetchResponse(modelUsage);

    const result = await fetchAuditUserUsage(3, "7d");
    expect(result).toEqual(modelUsage);
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/audit/usage/3?period=7d");
  });

  it("uses default period 7d", async () => {
    globalThis.fetch = mockFetchResponse([]);

    await fetchAuditUserUsage(1);
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/audit/usage/1?period=7d");
  });

  it("returns empty array on error", async () => {
    globalThis.fetch = mockFetchResponse(null, false, 404);
    const result = await fetchAuditUserUsage(99, "24h");
    expect(result).toEqual([]);
  });
});

describe("fetchAuditCaptures", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("calls /api/audit/captures with user_id, limit, offset", async () => {
    const captures = [
      { id: 1, session_id: 10, model: "gpt-4o", req_path: "/v1/chat/completions", created_at: "2025-01-01T00:00:00Z" },
    ];
    globalThis.fetch = mockFetchResponse(captures);

    const result = await fetchAuditCaptures(1, 20, 10);
    expect(result).toEqual(captures);
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/audit/captures?user_id=1&limit=20&offset=10",
    );
  });

  it("uses default limit and offset", async () => {
    globalThis.fetch = mockFetchResponse([]);

    await fetchAuditCaptures(5);
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/audit/captures?user_id=5&limit=50&offset=0",
    );
  });

  it("returns empty array on error", async () => {
    globalThis.fetch = mockFetchResponse(null, false, 500);
    const result = await fetchAuditCaptures(1);
    expect(result).toEqual([]);
  });

  it("returns empty array on network failure", async () => {
    globalThis.fetch = vi.fn(() => Promise.reject(new Error("timeout")));
    const result = await fetchAuditCaptures(1);
    expect(result).toEqual([]);
  });
});
