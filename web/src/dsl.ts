export type Entry =
  | { kind: 'map'; find: string; replace: string }
  | { kind: 'case'; op: 'lower' | 'upper' }

function isEscaped(s: string, i: number): boolean {
  let backslashes = 0
  for (let j = i - 1; j >= 0 && s[j] === '\\'; j--) backslashes++
  return backslashes % 2 === 1
}

// splitUnescaped splits on sep characters that are not backslash-escaped.
export function splitUnescaped(s: string, sep: string): string[] {
  const out: string[] = []
  let start = 0
  for (let i = 0; i < s.length; i++) {
    if (s[i] === sep && !isEscaped(s, i)) {
      out.push(s.slice(start, i))
      start = i + 1
    }
  }
  out.push(s.slice(start))
  return out
}

export function splitEntries(dsl: string): Entry[] {
  const entries: Entry[] = []
  for (const raw of splitUnescaped(dsl, ';')) {
    if (raw === '') continue
    if (raw === '\\a') { entries.push({ kind: 'case', op: 'lower' }); continue }
    if (raw === '\\A') { entries.push({ kind: 'case', op: 'upper' }); continue }
    // first unescaped "=>"
    for (let i = 0; i + 1 < raw.length; i++) {
      if (raw[i] === '=' && raw[i + 1] === '>' && !isEscaped(raw, i)) {
        entries.push({ kind: 'map', find: raw.slice(0, i), replace: raw.slice(i + 2) })
        break
      }
    }
  }
  return entries
}

export function joinEntries(entries: Entry[]): string {
  return entries
    .map((e) => {
      if (e.kind === 'case') return e.op === 'lower' ? '\\a' : '\\A'
      // Skip incomplete map rows so "Add mapping" placeholders never produce
      // empty find/replace (which parseRules rejects as invalid).
      if (!e.find.trim() || !e.replace.trim()) return ''
      return `${e.find}=>${e.replace}`
    })
    .filter(Boolean)
    .join(';')
}
