// Package verify 是独立验证套件（verifier 维护）。
//
// 目录：
//   - http/   HTTP 黑盒与契约探测（需要 platform/web 可编译）
//   - schema/ schema/字段/约束/PRAGMA/权限现状探测（只需 store）
//   - store/  R1 存储层行为验证（只需 store）
//   - dbcheck/ 只读 DB 检查小工具
//
// 约定：
//   - 只做黑盒/契约探测，不修改生产代码，不断言内部列名（按 Lead 分诊）。
//   - R0 退出标准用硬断言；已分诊的缺口用 t.Skip("TRIAGED ...") 标记，
//     使 go test 保持绿，同时让缺口在 go test -v 中可见。
//   - 缺口修复后把对应 Skip 改成硬断言；每轮报告引用 doc/0.0.1 最初版/verification-rN.md。
package verify
