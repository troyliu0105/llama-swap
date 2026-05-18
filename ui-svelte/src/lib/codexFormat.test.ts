import { describe, it, expect } from "vitest";
import { formatResetSeconds, formatCacheRate, labelForWindow } from "./codexFormat";

describe("formatResetSeconds", () => {
  it("returns 'now' for zero", () => {
    expect(formatResetSeconds(0)).toBe("now");
  });

  it("returns 'now' for negative", () => {
    expect(formatResetSeconds(-10)).toBe("now");
  });

  it("formats minutes only", () => {
    expect(formatResetSeconds(300)).toBe("5m");
  });

  it("formats hours and minutes", () => {
    expect(formatResetSeconds(7260)).toBe("2h 1m");
  });

  it("formats exact hours", () => {
    expect(formatResetSeconds(3600)).toBe("1h 0m");
  });
});

describe("formatCacheRate", () => {
  it("formats 0 as 0.0%", () => {
    expect(formatCacheRate(0)).toBe("0.0%");
  });

  it("formats ratio as percentage", () => {
    expect(formatCacheRate(0.369)).toBe("36.9%");
  });

  it("formats 1.0 as 100.0%", () => {
    expect(formatCacheRate(1)).toBe("100.0%");
  });

  it("rounds to one decimal", () => {
    expect(formatCacheRate(0.47192)).toBe("47.2%");
  });
});

describe("labelForWindow", () => {
  it("formats without reset date when reset_at is 0", () => {
    const label = labelForWindow({ used_percent: 5.0, reset_after_seconds: 464400, reset_at: 0 });
    expect(label).toBe("5.0% used, resets in 129h 0m");
    expect(label).not.toContain("(");
  });

  it("appends reset date when reset_at is set", () => {
    const label = labelForWindow({ used_percent: 12, reset_after_seconds: 3600, reset_at: 1747785600 });
    expect(label).toMatch(/12\.0% used, resets in 1h 0m \(\d+\/\d+ \d{2}:\d{2}\)/);
  });

  it("pads hours and minutes with zero", () => {
    const label = labelForWindow({ used_percent: 1, reset_after_seconds: 60, reset_at: 1747746000 });
    expect(label).toMatch(/\(\d+\/\d+ \d{2}:\d{2}\)/);
  });
});
