export type SortDir = "asc" | "desc";

export interface SortState<K extends string = string> {
  key: K;
  dir: SortDir;
}

/**
 * Toggle sort: if same key, flip direction; otherwise set key with default dir.
 */
export function toggleSort<K extends string>(
  current: SortState<K>,
  key: K,
  defaultDir: SortDir = "desc",
): SortState<K> {
  if (current.key === key) {
    return { key, dir: current.dir === "asc" ? "desc" : "asc" };
  }
  return { key, dir: defaultDir };
}

/**
 * Sort an array by the given key. Supports a value accessor for computed columns.
 * Returns a new array (does not mutate).
 */
export function sortBy<T>(
  items: T[],
  state: SortState,
  accessor: (item: T, key: string) => string | number,
): T[] {
  const { key, dir } = state;
  const mul = dir === "asc" ? 1 : -1;
  return [...items].sort((a, b) => {
    const av = accessor(a, key);
    const bv = accessor(b, key);
    if (typeof av === "string" && typeof bv === "string") {
      return mul * av.localeCompare(bv);
    }
    return mul * ((av as number) - (bv as number));
  });
}

/**
 * Returns an arrow indicator for the sort header.
 */
export function sortIndicator<K extends string>(state: SortState<K>, key: K): string {
  if (state.key !== key) return "";
  return state.dir === "asc" ? " ↑" : " ↓";
}
