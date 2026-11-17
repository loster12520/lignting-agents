# v0.0.1 HTTP API 契约（冻结草案）

> 版本 v1，Lead 维护。平台实现与前端必须以本文件为准；变更需 Lead 批准并同步 core/web。
> 所有路径以 /api 开头；除 /api/health 与 /api/setup 外都需要会话。

## 1. 通用

### 1.1 响应包

成功：

~~~json
{"ok": true, "data": {...}}
~~~

失败：HTTP 4xx/5xx + 错误包（不泄露内部堆栈）：

~~~json
{"ok": false, "error": {"code": "forbidden", "message": "没有权限", "details": {}}}
~~~

列表统一：

~~~json
{"ok": true, "data": {"items": [], "next": "", "total": 0}}
~~~

### 1.2 认证与 CSRF

- 首次启动：GET /api/bootstrap 返回 setup_required=true。
- POST /api/setup 创建 owner + 助理 + DM，设置 HttpOnly/SameSite=Strict 的 lignting_session Cookie。
- 写操作（POST/PUT/PATCH/DELETE）必须带 X-Lignting-CSRF，值来自 bootstrap/setup 响应；同时校验 Origin/Host。
- GET /api/events 也需要 Origin/Host 校验；SSE 不要求 CSRF，但要求会话。
- v0.0.1 单用户：session 只对应 owner；Agent 使用独立 Bearer Token 调用内部 API（仅 core 进程内使用）。

### 1.3 时间/ID

- 时间统一 RFC3339 UTC 字符串。
- ID 为带前缀字符串（如 msg_、task_、dept_）。
- 消息分页：?before=<seq>&limit=50 或 ?after=<seq>&limit=50，返回按 seq 升序。

## 2. 启动与设置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/health | 健康检查，无需登录 |
| GET | /api/bootstrap | 返回 setup_required、版本、csrf_token、owner、settings、agents、departments、conversations |
| POST | /api/setup | 首次配置：owner_name、deepseek_api_key（可空）、daily_budget_cny、monthly_budget_cny；返回 bootstrap 同构 |
| POST | /api/login | 本地密码登录（可选；默认本地自动会话） |
| POST | /api/logout | 清 Cookie |
| GET | /api/settings | 配置（Key 脱敏） |
| PUT | /api/settings | 修改配置；api_key 单独端点 |

### setup 请求

~~~json
{"owner_name":"阿哲","deepseek_api_key":"sk-...","daily_budget_cny":20,"monthly_budget_cny":300}
~~~

## 3. 会话与消息

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/conversations | 列表；?type=&department_id=&include_archived= |
| POST | /api/conversations | 创建；{type,title,department_id,visibility,member_ids,expires_at} |
| GET | /api/conversations/{id} | 详情 + 成员 |
| PATCH | /api/conversations/{id} | 改标题/可见性/成员/到期 |
| GET | /api/conversations/{id}/messages | 消息列表；?before=&after=&limit= |
| POST | /api/conversations/{id}/messages | 发消息；{content,type,thread_id,meta,client_id}；client_id 幂等 |
| POST | /api/conversations/{id}/read | 更新已读位置 {seq} |
| PATCH | /api/messages/{id} | 编辑内容（权限/时间限制） |
| DELETE | /api/messages/{id} | 软删除 + 墓碑 |
| POST | /api/conversations/{id}/join | 用户加入（观察者→成员） |
| POST | /api/conversations/{id}/leave | 用户退出 |

消息 DTO：

~~~json
{"seq":12,"id":"msg_...","conversation_id":"conv_...","thread_id":"","sender_type":"user","sender_id":"owner","sender_name":"阿哲","type":"text","content":"...","meta":"{}","created_at":"...","edited_at":"","deleted_at":""}
~~~

## 4. SSE

GET /api/events?since=<seq>

~~~text
id: 42
event: message.created
data: {"seq":42,"type":"message.created","conversation_id":"conv_...","object_type":"message","object_id":"msg_...","payload":"{...}","created_at":"..."}

: ping
~~~

- 断线重连带 Last-Event-ID 或 since，服务端补发 seq > since 的事件。
- 事件类型：message.created、message.updated、message.deleted、conversation.created、agent.status、task.updated、approval.updated、write.updated、notification.created。
- 心跳 20s。

## 5. 搜索

GET /api/search?q=限流&kind=message,task,decision,artifact&department_id=&conversation_id=&sender_id=&from=&to=&limit=20

返回：

~~~json
{"ok":true,"data":{"items":[{"kind":"message","ref_id":"msg_...","conversation_id":"conv_...","title":"","snippet":"...<em>限流</em>...","score":1.2,"created_at":"..."}],"total":1,"next":""}}
~~~

## 6. 组织与 Agent

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/org | 组织图：owner/assistant/departments/agents |
| GET | /api/departments | 部门列表 |
| GET | /api/departments/{id} | 部门详情 + 章程 + 员工 |
| POST | /api/departments | 创建（Lead/助理；需 G1 已批准） |
| POST | /api/departments/{id}/pause | 暂停 |
| POST | /api/departments/{id}/resume | 恢复 |
| GET | /api/agents | Agent 列表；?department_id=&kind= |
| GET | /api/agents/{id} | 角色卡/视角/权限/预算/最近行为 |
| POST | /api/agents/{id}/pause | 暂停 |
| POST | /api/agents/{id}/resume | 恢复 |
| POST | /api/agents/{id}/stop | 停止当前 turn |
| PATCH | /api/agents/{id} | 改名字/角色卡/预算（高风险需审批） |

## 7. 蓝图与 Provisioning

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /api/intents | 保存意图卡；{content}，版本 +1 |
| GET | /api/intents/latest | 最新意图卡 |
| POST | /api/blueprints | 生成蓝图；{intent_id,content}；创建 G1 approval |
| GET | /api/blueprints/{id} | 蓝图详情 |
| POST | /api/blueprints/{id}/approve | 用户 G1 批准 |
| POST | /api/provisioning | {blueprint_id}；幂等 |
| GET | /api/provisioning/{id} | 状态/步骤/错误 |

## 8. 任务/产出/评审/审批

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/tasks | ?status=&department_id=&owner_id= |
| POST | /api/tasks | 创建（DoR 校验） |
| GET | /api/tasks/{id} | 详情 + criteria + deps + events |
| POST | /api/tasks/{id}/transition | {to,reason,evidence} 状态机校验 |
| POST | /api/tasks/{id}/criteria/{cid}/evidence | 填证据 |
| POST | /api/tasks/{id}/criteria/{cid}/verdict | 主管结论 |
| POST | /api/tasks/{id}/g3 | 提交 G3（缺证据拒绝） |
| GET | /api/artifacts | ?task_id=&type= |
| POST | /api/artifacts | {type,title,task_id,content} 创建 + v1 |
| POST | /api/artifacts/{id}/versions | 新版本（不可变） |
| GET | /api/artifacts/{id}/versions/{v} | 版本内容 |
| GET | /api/reviews | ?status=&artifact_id= |
| POST | /api/reviews | 挑战单 |
| POST | /api/reviews/{id}/verdicts | 裁决 |
| GET | /api/decisions | ?q=&status= |
| POST | /api/decisions | 决策记录 |
| GET | /api/approvals | ?status=&kind= |
| POST | /api/approvals/{id}/decide | {decision:"approved"| "rejected",reason} |

## 9. 记忆/写工具/审计/成本

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/memories | ?scope=&department_id=&status=&q= |
| POST | /api/memories | 候选/直接创建（权限） |
| PATCH | /api/memories/{id} | 编辑 |
| DELETE | /api/memories/{id} | 墓碑删除 |
| POST | /api/memories/{id}/approve | 批准候选 |
| POST | /api/memories/{id}/reject | 拒绝候选 |
| GET | /api/manuals | 手册版本 |
| POST | /api/manuals | 新版本 |
| GET | /api/writes/timeline | ?path=&limit= |
| GET | /api/writes/{id} | 写事件 + diff |
| POST | /api/writes | 内部/测试创建写事件 |
| GET | /api/audit | ?object_type=&action=&since= |
| GET | /api/usage | ?group_by=department|task|agent|model&from=&to= |
| POST | /api/admin/backup | 备份 |
| POST | /api/admin/export | 导出 |
| POST | /api/admin/reindex | 重建索引 |
| POST | /api/admin/emergency-stop | 全局紧急停止 |

## 10. 前端需要的错误/空态

- 401：跳登录/首次向导。
- 403：显示「没有权限 + 可行路径」。
- 409：冲突时给「重新读取/重试」。
- 503：模型不可用时显示降级提示，不阻塞浏览/搜索。

## 11. 实现顺序

1. R1 先实现 §2/§3/§4/§5 + §6 只读。
2. R3 实现 §6 写 + §7。
3. R4 实现 §8。
4. R5 实现 §9 写工具部分。
5. R7 实现 §9 记忆部分。
6. R8 实现 §9 审计/成本/备份。
