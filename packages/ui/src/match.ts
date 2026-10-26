// Search: whether a row's fields match what the Operator typed.

const mac = /^[0-9a-f]{2}([:-][0-9a-f]{2}){5}$/i;
const separators = /[:-]/g;

/**
 * Whether a search query matches a row's fields, e.g. a Device's name, IP
 * and MAC: every word of the query, ignoring case, appears in some field.
 * A MAC also matches without its separators, so "aabb" finds aa:bb:…. An
 * empty query matches everything; undefined fields are skipped.
 */
export function matches(query: string, ...fields: readonly (string | undefined)[]): boolean {
  const haystacks = fields.flatMap((f) => {
    if (f === undefined) return [];
    const lower = f.toLowerCase();
    return mac.test(f) ? [lower, lower.replace(separators, "")] : [lower];
  });
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((word) => {
      const bare = word.replace(separators, "");
      return haystacks.some((h) => h.includes(word) || (bare !== "" && h.includes(bare)));
    });
}
