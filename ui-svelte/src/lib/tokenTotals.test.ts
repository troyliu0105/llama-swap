import { describe, it, expect } from "vitest";
import { sumTokenTotals, sumTokenTotalsWithRequests } from "./tokenTotals";

describe("sumTokenTotals", () => {
  it("returns zeros for empty array", () => {
    expect(sumTokenTotals([])).toEqual({
      input_tokens: 0,
      output_tokens: 0,
      cached_tokens: 0,
      input_plus_output: 0,
    });
  });

  it("sums single entry", () => {
    const entries = [{ input_tokens: 100, output_tokens: 200, cached_tokens: 50 }];
    expect(sumTokenTotals(entries)).toEqual({
      input_tokens: 100,
      output_tokens: 200,
      cached_tokens: 50,
      input_plus_output: 300,
    });
  });

  it("sums multiple entries", () => {
    const entries = [
      { input_tokens: 100, output_tokens: 200, cached_tokens: 50 },
      { input_tokens: 300, output_tokens: 400, cached_tokens: 150 },
      { input_tokens: 50, output_tokens: 75, cached_tokens: 25 },
    ];
    expect(sumTokenTotals(entries)).toEqual({
      input_tokens: 450,
      output_tokens: 675,
      cached_tokens: 225,
      input_plus_output: 1125,
    });
  });

  it("handles zero values", () => {
    const entries = [
      { input_tokens: 0, output_tokens: 0, cached_tokens: 0 },
      { input_tokens: 10, output_tokens: 20, cached_tokens: 0 },
    ];
    expect(sumTokenTotals(entries)).toEqual({
      input_tokens: 10,
      output_tokens: 20,
      cached_tokens: 0,
      input_plus_output: 30,
    });
  });
});

describe("sumTokenTotalsWithRequests", () => {
  it("returns zeros for empty array", () => {
    expect(sumTokenTotalsWithRequests([])).toEqual({
      input_tokens: 0,
      output_tokens: 0,
      cached_tokens: 0,
      input_plus_output: 0,
      request_count: 0,
    });
  });

  it("sums entries including request_count", () => {
    const entries = [
      { input_tokens: 100, output_tokens: 200, cached_tokens: 50, request_count: 5 },
      { input_tokens: 300, output_tokens: 400, cached_tokens: 150, request_count: 10 },
    ];
    expect(sumTokenTotalsWithRequests(entries)).toEqual({
      input_tokens: 400,
      output_tokens: 600,
      cached_tokens: 200,
      input_plus_output: 1000,
      request_count: 15,
    });
  });
});
