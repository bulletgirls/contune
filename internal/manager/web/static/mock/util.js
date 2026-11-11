// 결정적 난수·ULID·cron·시간 도구. DOM과 window에 기대지 않는다.

export const SEC = 1000;
export const MIN = 60000;
export const HOUR = 3600000;
export const DAY = 86400000;
export const KST = 9 * HOUR;

export function hash32(str) {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  h ^= h >>> 15;
  h = Math.imul(h, 2246822507) >>> 0;
  h ^= h >>> 13;
  h = Math.imul(h, 3266489909) >>> 0;
  h ^= h >>> 16;
  return h >>> 0;
}

export function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 0~1
export function rnd(key) {
  return hash32(String(key)) / 4294967296;
}

// -1~1. 같은 key·ts면 같은 값
export function noise(key, ts) {
  return rnd(key + '|' + Math.floor(ts / 1000)) * 2 - 1;
}

export function clamp(x, lo, hi) {
  return x < lo ? lo : x > hi ? hi : x;
}

export function round(x, d = 1) {
  const p = Math.pow(10, d);
  return Math.round(x * p) / p;
}

// 키프레임 [[이전 초, 값], …]을 이전 초 내림차순으로 두고 선형 보간한다.
export function kf(table, sb) {
  if (sb >= table[0][0]) return table[0][1];
  const last = table[table.length - 1];
  if (sb <= last[0]) return last[1];
  for (let i = 0; i < table.length - 1; i++) {
    const [s0, v0] = table[i];
    const [s1, v1] = table[i + 1];
    if (sb <= s0 && sb >= s1) {
      const r = s0 === s1 ? 1 : (s0 - sb) / (s0 - s1);
      return v0 + (v1 - v0) * r;
    }
  }
  return last[1];
}

const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

// ULID 모양 26자. 시간부 10자(고정 기준 + idx) + 무작위부 16자(seed로 결정)
export const ULID_BASE = 1794000000000;
export function ulid(idx, seed) {
  let t = ULID_BASE + idx * 1000;
  let s = '';
  for (let i = 0; i < 10; i++) {
    s = CROCKFORD[t % 32] + s;
    t = Math.floor(t / 32);
  }
  const r = mulberry32(hash32(seed));
  for (let i = 0; i < 16; i++) s += CROCKFORD[Math.floor(r() * 32)];
  return s;
}

export function fmtKst(ts) {
  const d = new Date(ts + KST);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}`;
}

export function fmtN(n) {
  return Math.round(n).toLocaleString('en-US');
}

export function parseQuery(path) {
  const i = path.indexOf('?');
  const pathname = i < 0 ? path : path.slice(0, i);
  const q = new URLSearchParams(i < 0 ? '' : path.slice(i + 1));
  return { pathname, q };
}

// ---- cron (분 시 * * *, 분 시 * * 1-5, */n * * * *만. strict가 아니면 일(日) 숫자도) ----

function parseField(f, lo, hi) {
  if (f === '*') return { any: true };
  let m = /^\*\/(\d+)$/.exec(f);
  if (m && +m[1] > 0) return { step: +m[1] };
  m = /^(\d+)$/.exec(f);
  if (m && +m[1] >= lo && +m[1] <= hi) return { vals: [+m[1]] };
  m = /^(\d+)-(\d+)$/.exec(f);
  if (m && +m[1] >= lo && +m[2] <= hi && +m[1] <= +m[2]) {
    const vals = [];
    for (let v = +m[1]; v <= +m[2]; v++) vals.push(v);
    return { vals };
  }
  return null;
}

function fieldHas(spec, v) {
  if (spec.any) return true;
  if (spec.step) return v % spec.step === 0;
  return spec.vals.includes(v);
}

export function parseCron(expr, strict = true) {
  const f = String(expr || '').trim().split(/\s+/);
  if (f.length !== 5) return null;
  const [mi, ho, dom, mon, dow] = f;
  const m = parseField(mi, 0, 59);
  const h = parseField(ho, 0, 23);
  if (!m || !h) return null;
  if (m.step && !h.any) return null;
  if (mon !== '*') return null;
  let d = { any: true };
  if (dom !== '*') {
    if (strict) return null;
    d = parseField(dom, 1, 31);
    if (!d || d.step) return null;
  }
  let w = { any: true };
  if (dow !== '*') {
    w = parseField(dow, 0, 7);
    if (!w || w.step) return null;
    if (strict && !/^\d-\d$/.test(dow)) return null;
  }
  return { m, h, d, w };
}

// 다음 n번의 발화 시각(ms). 지원하지 않는 표현이면 []
export function cronNext(expr, from, n = 3, strict = true) {
  const c = parseCron(expr, strict);
  if (!c) return [];
  const out = [];
  let t = Math.floor(from / MIN) * MIN + MIN;
  for (let i = 0; i < 40 * 24 * 60 && out.length < n; i++, t += MIN) {
    const d = new Date(t + KST);
    if (!fieldHas(c.m, d.getUTCMinutes())) continue;
    if (!fieldHas(c.h, d.getUTCHours())) continue;
    if (!fieldHas(c.d, d.getUTCDate())) continue;
    const dw = d.getUTCDay();
    if (!(fieldHas(c.w, dw) || (dw === 0 && fieldHas(c.w, 7)))) continue;
    out.push(t);
  }
  return out;
}

export function sha(str) {
  return hash32(str).toString(16).padStart(8, '0') + hash32('x' + str).toString(16).padStart(8, '0');
}

// ---- 아주 작은 YAML(플로우 정의에 쓰는 부분만) ----

function splitTop(s) {
  const out = [];
  let depth = 0;
  let q = '';
  let cur = '';
  for (const ch of s) {
    if (q) {
      cur += ch;
      if (ch === q) q = '';
    } else if (ch === '"' || ch === "'") {
      q = ch;
      cur += ch;
    } else if (ch === '[' || ch === '{') {
      depth++;
      cur += ch;
    } else if (ch === ']' || ch === '}') {
      depth--;
      cur += ch;
    } else if (ch === ',' && depth === 0) {
      out.push(cur);
      cur = '';
    } else cur += ch;
  }
  if (cur.trim() !== '') out.push(cur);
  return out;
}

function scalar(s) {
  s = s.trim();
  if (s === '') return null;
  if (s[0] === '[' && s.endsWith(']')) return splitTop(s.slice(1, -1)).map(scalar);
  if (s[0] === '{' && s.endsWith('}')) {
    const o = {};
    for (const part of splitTop(s.slice(1, -1))) {
      const k = part.indexOf(':');
      if (k < 0) continue;
      o[part.slice(0, k).trim()] = scalar(part.slice(k + 1));
    }
    return o;
  }
  if ((s[0] === '"' && s.endsWith('"')) || (s[0] === "'" && s.endsWith("'"))) {
    return s.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
  }
  if (s === 'true') return true;
  if (s === 'false') return false;
  if (s === 'null' || s === '~') return null;
  if (/^-?\d+(\.\d+)?$/.test(s)) return Number(s);
  return s;
}

export function parseYaml(text) {
  const lines = String(text)
    .split('\n')
    .map((raw) => ({ indent: raw.search(/\S/), text: raw.trim() }))
    .filter((l) => l.text && !l.text.startsWith('#'));
  let i = 0;
  const isList = (l) => l.text === '-' || l.text.startsWith('- ');
  function block(indent) {
    return isList(lines[i]) ? list(indent) : map(indent);
  }
  function list(indent) {
    const arr = [];
    while (i < lines.length && lines[i].indent === indent && isList(lines[i])) {
      const rest = lines[i].text.slice(1).trimStart();
      if (!rest) {
        i++;
        arr.push(i < lines.length && lines[i].indent > indent ? block(lines[i].indent) : null);
      } else if (/^[A-Za-z_][\w-]*:(\s|$)/.test(rest)) {
        lines[i] = { indent: indent + (lines[i].text.length - rest.length), text: rest };
        arr.push(map(lines[i].indent));
      } else {
        arr.push(scalar(rest));
        i++;
      }
    }
    return arr;
  }
  function map(indent) {
    const o = {};
    while (i < lines.length && lines[i].indent === indent && !isList(lines[i])) {
      const t = lines[i].text;
      const k = t.search(/:(\s|$)/);
      if (k < 0) throw new Error(`YAML 줄을 읽을 수 없음: ${t}`);
      const key = t.slice(0, k).trim();
      const val = t.slice(k + 1).trim();
      i++;
      if (val === '') {
        if (i < lines.length && lines[i].indent > indent) o[key] = block(lines[i].indent);
        else if (i < lines.length && lines[i].indent === indent && isList(lines[i])) o[key] = list(indent);
        else o[key] = null;
      } else o[key] = scalar(val);
    }
    return o;
  }
  if (!lines.length) return {};
  return block(lines[0].indent);
}

function dumpScalar(v) {
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  const s = String(v);
  if (/^[A-Za-z0-9_./@%+=-]+$/.test(s) && !/^(true|false|null|~)$/.test(s)) return s;
  return '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

export function dumpFlow(f) {
  const L = [];
  L.push(`id: ${dumpScalar(f.id)}`);
  if (f.schedule) L.push(`schedule: ${dumpScalar(f.schedule)}`);
  if (f.on_missed) L.push(`on_missed: ${dumpScalar(f.on_missed)}`);
  L.push('steps:');
  for (const s of f.steps || []) {
    L.push(`  - id: ${dumpScalar(s.id)}`);
    L.push(`    host: ${dumpScalar(s.host)}`);
    L.push(`    command: [${(s.command || []).map(dumpScalar).join(', ')}]`);
    if (s.depends && s.depends.length) L.push(`    depends: [${s.depends.map(dumpScalar).join(', ')}]`);
    if (s.timeout) L.push(`    timeout: ${dumpScalar(s.timeout)}`);
    if (s.retry && (s.retry.count || s.retry.interval)) {
      L.push(`    retry: { count: ${s.retry.count || 0}, interval: ${dumpScalar(s.retry.interval || '1m')} }`);
    }
    if (s.profile) L.push('    profile: true');
    if (s.gate && s.gate.length) {
      L.push('    gate:');
      for (const g of s.gate) {
        const kv = ['host', 'metric', 'max', 'min'].filter((k) => g[k] != null && g[k] !== '').map((k) => `${k}: ${dumpScalar(g[k])}`);
        L.push(`      - { ${kv.join(', ')} }`);
      }
    }
    if (s.gate_max_wait) L.push(`    gate_max_wait: ${dumpScalar(s.gate_max_wait)}`);
  }
  return L.join('\n') + '\n';
}
