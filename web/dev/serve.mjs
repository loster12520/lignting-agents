// dev 静态 + API 反代（仅本地联调用，不参与 embed）
// 用途：主干 Go 构建临时失败时，用磁盘上的 web/ 资源 + 已运行的旧二进制 API 做浏览器验收。
// 用法：node web/dev/serve.mjs <listenPort> <apiBase>
import { createServer, request as httpRequest } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { extname, join, normalize } from "node:path";

const listenPort = Number(process.argv[2] || 18797);
const apiBase = new URL(process.argv[3] || "http://127.0.0.1:18796");
const ROOT = new URL("../", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
// ../ 已指向 web/ 本身；再 join("web") 会变成 web/web，导致 /assets/* 404。
const WEB = ROOT.replace(/[\\/]+$/, "");

const MIME = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".png": "image/png", ".svg": "image/svg+xml" };

function proxy(req, res) {
  const up = httpRequest({
    hostname: apiBase.hostname, port: apiBase.port, path: req.url, method: req.method,
    headers: Object.assign({}, req.headers, { host: apiBase.host })
  }, (ur) => {
    res.writeHead(ur.statusCode || 502, ur.headers);
    ur.pipe(res);
  });
  up.on("error", (e) => { res.writeHead(502, { "Content-Type": "text/plain" }); res.end("proxy error: " + e.message); });
  req.pipe(up);
}

createServer(async (req, res) => {
  try {
    const path = decodeURIComponent((req.url || "/").split("?")[0]);
    if (path.startsWith("/api/")) { proxy(req, res); return; }
    let file;
    if (path === "/") file = join(WEB, "index.html");
    else if (path.startsWith("/assets/")) {
      const name = normalize(path.slice("/assets/".length));
      if (name.indexOf("..") >= 0) { res.writeHead(404); res.end(); return; }
      file = join(WEB, name);
    } else { res.writeHead(404); res.end(); return; }
    const buf = await readFile(file);
    res.writeHead(200, { "Content-Type": MIME[extname(file)] || "application/octet-stream", "Cache-Control": "no-cache" });
    res.end(buf);
  } catch (e) {
    res.writeHead(404, { "Content-Type": "text/plain" });
    res.end("not found");
  }
}).listen(listenPort, "127.0.0.1", () => {
  console.log("web dev server: http://127.0.0.1:" + listenPort + "  api->" + apiBase.origin);
});
