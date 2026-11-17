package web

import "embed"

// 工作台前端资源。零构建：原生 HTML/CSS/ES Module，直接嵌入二进制。
//
//go:embed index.html app.js *.css lib views
var FS embed.FS
