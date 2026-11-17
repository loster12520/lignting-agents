// Markdown 解析器（纯函数，无 DOM 依赖）。
//
// 设计目标：聊天消息里的基础 Markdown，安全、可流式、可测试。
// 输出是 token 树，渲染由 lib/mdrender.js 用 DOM 节点重建 —— 全过程不产生 HTML 字符串。
//
// 支持：标题 #..######、粗体 **x** / __x__、斜体 *x* / _x_、行内代码、
//      代码围栏（带语言）、有序/无序列表、引用、分隔线、简单表格、
//      链接 [t](u)、裸 URL、@提及、反斜杠转义、换行。
// 不认：HTML 标签（按字面文本）、图片、脚注、嵌套列表缩进语义。

const BT = String.fromCharCode(96);
const FENCE = BT + BT + BT;
const ESCAPE_CHARS = "\\*_[]()#-+.!>|\u0060~$";

const RE_HEADING = /^(#{1,6})\s+(.*)$/;
const RE_UL = /^[-*+]\s+(.*)$/;
const RE_OL = /^(\d{1,3})[.)]\s+(.*)$/;
const RE_HR = /^([-*_])\1{2,}\s*$/;
const RE_TABLE_SEP = /^\s*\|?[\s:|-]+\|[\s:|-]*$/;

function isBlank(line) { return /^\s*$/.test(line); }

function countFence(line) {
  const t = line.replace(/^\s+/, "");
  let n = 0;
  while (n < t.length && t.charCodeAt(n) === 96) n++;
  return n >= 3 ? n : 0;
}

function startsBlock(line) {
  if (isBlank(line)) return true;
  if (countFence(line) > 0) return true;
  if (RE_HR.test(line)) return true;
  if (/^\s*>\s?/.test(line)) return true;
  if (RE_UL.test(line.replace(/^\s+/, ""))) return true;
  if (RE_OL.test(line.replace(/^\s+/, ""))) return true;
  return false;
}

function splitRow(line) {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  const cells = [];
  let cur = "";
  let escaped = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (escaped) { cur += ch; escaped = false; continue; }
    if (ch === "\\") { cur += ch; escaped = true; continue; }
    if (ch === "|") { cells.push(cur.trim()); cur = ""; continue; }
    cur += ch;
  }
  cells.push(cur.trim());
  return cells;
}

function looksLikeTable(lines, i) {
  if (i + 1 >= lines.length) return false;
  if (lines[i].indexOf("|") < 0) return false;
  const sep = lines[i + 1];
  if (!RE_TABLE_SEP.test(sep)) return false;
  return sep.indexOf("-") >= 0;
}

export function parseMarkdown(src) {
  const input = String(src == null ? "" : src).replace(/\r\n?/g, "\n");
  const lines = input.split("\n");
  const blocks = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (isBlank(line)) { i++; continue; }

    const fenceLen = countFence(line);
    if (fenceLen > 0) {
      const lang = line.replace(/^\s+/, "").slice(fenceLen).trim();
      const buf = [];
      i++;
      let closed = false;
      while (i < lines.length) {
        if (countFence(lines[i]) >= fenceLen) { closed = true; i++; break; }
        buf.push(lines[i]);
        i++;
      }
      blocks.push({ t: "code", lang: lang, code: buf.join("\n"), closed: closed });
      continue;
    }

    if (RE_HR.test(line)) { blocks.push({ t: "hr" }); i++; continue; }

    const hm = RE_HEADING.exec(line);
    if (hm) {
      blocks.push({ t: "h", level: hm[1].length, inl: parseInline(hm[2]) });
      i++;
      continue;
    }

    if (/^\s*>\s?/.test(line)) {
      const buf = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ""));
        i++;
      }
      blocks.push({ t: "quote", blocks: parseMarkdown(buf.join("\n")) });
      continue;
    }

    if (looksLikeTable(lines, i)) {
      const head = splitRow(lines[i]).map(parseInline);
      i += 2;
      const rows = [];
      while (i < lines.length && !isBlank(lines[i]) && lines[i].indexOf("|") >= 0) {
        rows.push(splitRow(lines[i]).map(parseInline));
        i++;
      }
      blocks.push({ t: "table", head: head, rows: rows });
      continue;
    }

    const ulm = RE_UL.exec(line.replace(/^\s+/, ""));
    if (ulm) {
      const items = [];
      while (i < lines.length) {
        const raw = lines[i];
        const m = RE_UL.exec(raw.replace(/^\s+/, ""));
        if (m) { items.push([m[1]]); i++; continue; }
        if (isBlank(raw) || startsBlock(raw)) break;
        if (items.length > 0) { items[items.length - 1].push(raw.trim()); i++; continue; }
        break;
      }
      blocks.push({ t: "ul", items: items.map(function (parts) { return parseInline(parts.join("\n")); }) });
      continue;
    }

    const olm = RE_OL.exec(line.replace(/^\s+/, ""));
    if (olm) {
      const start = parseInt(olm[1], 10) || 1;
      const items = [];
      while (i < lines.length) {
        const raw = lines[i];
        const m = RE_OL.exec(raw.replace(/^\s+/, ""));
        if (m) { items.push([m[2]]); i++; continue; }
        if (isBlank(raw) || startsBlock(raw)) break;
        if (items.length > 0) { items[items.length - 1].push(raw.trim()); i++; continue; }
        break;
      }
      blocks.push({ t: "ol", start: start, items: items.map(function (parts) { return parseInline(parts.join("\n")); }) });
      continue;
    }

    const para = [line.trim()];
    i++;
    while (i < lines.length && !isBlank(lines[i]) && !startsBlock(lines[i]) && !RE_HEADING.test(lines[i])) {
      para.push(lines[i].trim());
      i++;
    }
    blocks.push({ t: "p", inl: parseInline(para.join("\n")) });
  }

  return blocks;
}

const MENTION_RE = /^@([\p{L}\p{N}_.-]{1,32})/u;

function isUrlChar(ch) {
  return /[A-Za-z0-9]/.test(ch) || "/?#&=%+~-_.!*'();:@$,[]".indexOf(ch) >= 0;
}

export function parseInline(src) {
  return parseInlineDepth(String(src == null ? "" : src), 0);
}

function parseInlineDepth(src, depth) {
  const out = [];
  let buf = "";

  function flush() {
    if (buf) { out.push({ t: "text", v: buf }); buf = ""; }
  }

  let i = 0;
  while (i < src.length) {
    const ch = src[i];

    if (ch === "\\" && i + 1 < src.length) {
      const nxt = src[i + 1];
      if (ESCAPE_CHARS.indexOf(nxt) >= 0) { buf += nxt; i += 2; continue; }
      buf += ch; i++; continue;
    }

    if (ch.charCodeAt(0) === 96) {
      let run = 0;
      while (i + run < src.length && src.charCodeAt(i + run) === 96) run++;
      const marker = BT.repeat(run);
      const close = src.indexOf(marker, i + run);
      if (close > 0) {
        flush();
        out.push({ t: "code", v: src.slice(i + run, close) });
        i = close + run;
        continue;
      }
      buf += marker; i += run; continue;
    }

    if ((ch === "*" || ch === "_") && src[i + 1] === ch) {
      const marker = ch + ch;
      const close = findCloser(src, i + 2, marker);
      if (close > 0 && depth < 3) {
        flush();
        out.push({ t: "strong", c: parseInlineDepth(src.slice(i + 2, close), depth + 1) });
        i = close + marker.length;
        continue;
      }
    }

    if (ch === "*" || ch === "_") {
      const close = findCloser(src, i + 1, ch);
      if (close > 0 && depth < 3) {
        flush();
        out.push({ t: "em", c: parseInlineDepth(src.slice(i + 1, close), depth + 1) });
        i = close + 1;
        continue;
      }
    }

    if (ch === "[") {
      const closeBracket = src.indexOf("]", i + 1);
      if (closeBracket > 0 && src[closeBracket + 1] === "(") {
        const closeParen = src.indexOf(")", closeBracket + 2);
        if (closeParen > 0) {
          const label = src.slice(i + 1, closeBracket);
          const href = src.slice(closeBracket + 2, closeParen).trim();
          if (safeHref(href)) {
            flush();
            out.push({ t: "link", href: href, c: parseInlineDepth(label, depth + 1) });
            i = closeParen + 1;
            continue;
          }
        }
      }
    }

    if ((ch === "h" || ch === "H") && /^https?:\/\//i.test(src.slice(i, i + 8))) {
      let j = i + 4;
      while (j < src.length && isUrlChar(src[j])) j++;
      let url = src.slice(i, j).replace(/[.,;:!?)\]]+$/, "");
      flush();
      out.push({ t: "link", href: url, c: [{ t: "text", v: url }] });
      i = i + url.length;
      continue;
    }

    if (ch === "@") {
      const m = MENTION_RE.exec(src.slice(i));
      if (m) {
        flush();
        out.push({ t: "mention", v: m[1] });
        i += m[0].length;
        continue;
      }
    }

    if (ch === "\n") { flush(); out.push({ t: "br" }); i++; continue; }

    buf += ch;
    i++;
  }
  flush();
  return out;
}

function findCloser(src, from, marker) {
  if (from >= src.length) return -1;
  const first = src[from];
  if (first === " " || first === "\n" || first === marker[0]) return -1;
  let idx = src.indexOf(marker, from + 1);
  while (idx > 0) {
    const prev = src[idx - 1];
    if (prev !== " " && prev !== "\n" && prev !== marker[0] && prev !== "\\") return idx;
    idx = src.indexOf(marker, idx + marker.length);
  }
  return -1;
}

export function safeHref(href) {
  const h = String(href || "").trim();
  if (!h) return false;
  if (/^https?:\/\//i.test(h)) return true;
  if (/^mailto:[^\s]+@[^\s]+$/i.test(h)) return true;
  if (h.startsWith("#/")) return true;
  return false;
}

// 供左侧列表/搜索预览使用：把 Markdown 压成一行纯文本
export function toPlainText(src, maxLen) {
  const blocks = parseMarkdown(src);
  const parts = [];
  for (let b = 0; b < blocks.length; b++) {
    parts.push(blocksToText([blocks[b]]));
    parts.push(" ");
  }
  const s = parts.join("").replace(/\s+/g, " ").trim();
  if (maxLen && s.length > maxLen) return s.slice(0, maxLen) + "…";
  return s;
}

function inlineToText(inl) {
  let s = "";
  for (let i = 0; i < inl.length; i++) {
    const n = inl[i];
    if (n.t === "text" || n.t === "code") s += n.v;
    else if (n.t === "mention") s += "@" + n.v;
    else if (n.t === "br") s += " ";
    else if (n.c) s += inlineToText(n.c);
  }
  return s;
}

function blocksToText(blocks) {
  let s = "";
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i];
    if (b.inl) s += inlineToText(b.inl);
    else if (b.t === "code") s += b.code;
    else if (b.items) {
      for (let k = 0; k < b.items.length; k++) s += inlineToText(b.items[k]) + " ";
    } else if (b.blocks) s += blocksToText(b.blocks);
    else if (b.t === "table") {
      for (let r = 0; r < b.rows.length; r++) s += b.rows[r].map(inlineToText).join(" ") + " ";
    }
    s += "\n";
  }
  return s;
}
