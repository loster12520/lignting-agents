// Markdown token 树 -> DOM。全程 createElement/textContent，不拼接 HTML 字符串。
import { el } from "./dom.js";
import { parseMarkdown } from "./markdown.js";

function renderInline(inl) {
  const out = [];
  for (let i = 0; i < inl.length; i++) {
    const n = inl[i];
    switch (n.t) {
      case "text": out.push(n.v); break;
      case "strong": out.push(el("strong", null, renderInline(n.c))); break;
      case "em": out.push(el("em", null, renderInline(n.c))); break;
      case "code": out.push(el("code", null, n.v)); break;
      case "br": out.push(el("br")); break;
      case "mention":
        out.push(el("span", { class: "mention-pill", text: "@" + n.v }));
        break;
      case "link": {
        const external = /^https?:\/\//i.test(n.href);
        const attrs = external
          ? { href: n.href, target: "_blank", rel: "noopener noreferrer" }
          : { href: n.href };
        out.push(el("a", { attrs: attrs }, renderInline(n.c)));
        break;
      }
      default: break;
    }
  }
  return out;
}

function renderBlock(b) {
  switch (b.t) {
    case "p": return el("p", null, renderInline(b.inl));
    case "h": {
      const level = Math.min(Math.max(b.level, 1), 4);
      return el("h" + level, null, renderInline(b.inl));
    }
    case "code": {
      const pre = el("pre", null, el("code", null, b.code));
      if (b.lang) pre.appendChild(el("span", { class: "code-lang", text: b.lang }));
      return pre;
    }
    case "ul": {
      const ul = el("ul");
      for (let i = 0; i < b.items.length; i++) ul.appendChild(el("li", null, renderInline(b.items[i])));
      return ul;
    }
    case "ol": {
      const ol = el("ol");
      if (b.start && b.start !== 1) ol.setAttribute("start", String(b.start));
      for (let i = 0; i < b.items.length; i++) ol.appendChild(el("li", null, renderInline(b.items[i])));
      return ol;
    }
    case "quote": {
      const q = el("blockquote");
      for (let i = 0; i < b.blocks.length; i++) q.appendChild(renderBlock(b.blocks[i]));
      return q;
    }
    case "hr": return el("hr");
    case "table": {
      const table = el("table");
      const thead = el("thead");
      const htr = el("tr");
      for (let i = 0; i < b.head.length; i++) {
        htr.appendChild(el("th", { attrs: { scope: "col" } }, renderInline(b.head[i])));
      }
      thead.appendChild(htr);
      table.appendChild(thead);
      if (b.rows.length) {
        const tbody = el("tbody");
        for (let r = 0; r < b.rows.length; r++) {
          const tr = el("tr");
          for (let c = 0; c < b.rows[r].length; c++) {
            tr.appendChild(el("td", null, renderInline(b.rows[r][c])));
          }
          tbody.appendChild(tr);
        }
        table.appendChild(tbody);
      }
      return table;
    }
    default: return el("p", { text: "" });
  }
}

export function renderMarkdown(src) {
  const wrap = el("div", { class: "md", attrs: { role: "document" } });
  const blocks = parseMarkdown(src);
  for (let i = 0; i < blocks.length; i++) wrap.appendChild(renderBlock(blocks[i]));
  return wrap;
}

export function renderSnippet(parts) {
  const wrap = el("div", { class: "result-snippet" });
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    if (p.t === "em") wrap.appendChild(el("em", null, p.v));
    else wrap.appendChild(document.createTextNode(p.v));
  }
  return wrap;
}
