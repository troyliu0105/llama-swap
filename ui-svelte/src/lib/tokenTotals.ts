export interface TokenFields {
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
}

export interface TokenFieldsWithRequests extends TokenFields {
  request_count: number;
}

export interface TokenTotals {
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  input_plus_output: number;
}

export interface TokenTotalsWithRequests extends TokenTotals {
  request_count: number;
}

export function sumTokenTotals(entries: TokenFields[]): TokenTotals {
  const input_tokens = entries.reduce((s, e) => s + e.input_tokens, 0);
  const output_tokens = entries.reduce((s, e) => s + e.output_tokens, 0);
  const cached_tokens = entries.reduce((s, e) => s + e.cached_tokens, 0);
  return {
    input_tokens,
    output_tokens,
    cached_tokens,
    input_plus_output: input_tokens + output_tokens,
  };
}

export function sumTokenTotalsWithRequests(entries: TokenFieldsWithRequests[]): TokenTotalsWithRequests {
  const totals = sumTokenTotals(entries);
  return {
    ...totals,
    request_count: entries.reduce((s, e) => s + e.request_count, 0),
  };
}
