// 搜索片段解析器（纯函数，无 DOM 依赖，可在 node 下直接测试）
//
// 后端契约：snippet 形如 "我们下周<em>部署</em>第一版"，即「正文 HTML 转义 + 仅 <em> 标记命中词」。
// 浏览器里绝不能把整段当 HTML 注入，因此这里把它拆成文本片段，
// 由渲染层用文本节点 + <em> 元素重建。未识别的标签一律按字面文本处理。

const ENTITIES = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  "#39": "'",
  apos: "'",
  nbsp: " "
};

export function decodeEntities(s) {
  return s.replace(/&(#\d+|#x[0-9a-fA-F]+|[a-zA-Z]+);/g, (all, name) => {
    if (name[0] === "#") {
      let code = 0;
      if (name[1] === "x" || name[1] === "X") code = parseInt(name.slice(2), 16);
      else code = parseInt(name.slice(1), 10);
      if (!Number.isFinite(code) || code <= 0 || code > 0x10ffff) return all;
      try { return String.fromCodePoint(code); } catch (e) { return all; }
    }
    const lower = name.toLowerCase();
    if (Object.prototype.hasOwnProperty.call(ENTITIES, lower)) return ENTITIES[lower];
    return all;
  });
}

// parseSnippet('我们下周<em>部署</em>第一版')
//   -> [{t:'text', v:'我们下周'}, {t:'em', v:'部署'}, {t:'text', v:'第一版'}]
export function parseSnippet(raw) {
  const out = [];
  if (raw == null) return out;
  const src = String(raw);

  let i = 0;
  let buf = "";
  const flush = () => {
    if (buf) { out.push({ t: "text", v: decodeEntities(buf) }); buf = ""; }
  };

  while (i < src.length) {
    const lt = src.indexOf("<", i);
    if (lt < 0) { buf += src.slice(i); break; }
    buf += src.slice(i, lt);

    // 只认 <em> 与 </em>（大小写不敏感，允许 <em > 这种空白）
    const rest = src.slice(lt);
    const open = /^<em\s*>/i.exec(rest);
    const close = /^<\/em\s*>/i.exec(rest);

    if (open) {
      flush();
      const end = src.toLowerCase().indexOf("</em>", lt + open[0].length);
      if (end < 0) {
        // 未闭合：剩余全部作为命中片段（容忍流式截断）
        out.push({ t: "em", v: decodeEntities(src.slice(lt + open[0].length)) });
        i = src.length;
        break;
      }
      out.push({ t: "em", v: decodeEntities(src.slice(lt + open[0].length, end)) });
      i = end + 5;
      continue;
    }
    if (close) { i = lt + close[0].length; continue; }

    // 其它标签按字面文本处理，避免任何注入面
    buf += "<";
    i = lt + 1;
  }
  flush();

  if (out.length === 0) return [{ t: "text", v: "" }];
  return out;
}
