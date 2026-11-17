-- Lignting Agents v0.0.1 schema. Authoritative copy: internal/store/schema.sql
-- All timestamps are RFC3339 UTC strings. Optional IDs use '' (not NULL) for easy indexing.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS owner (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  token_hash    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS departments (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  kind       TEXT NOT NULL,
  mission    TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'active',
  charter    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_departments_status ON departments(status);

CREATE TABLE IF NOT EXISTS agents (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL,
  department_id   TEXT NOT NULL DEFAULT '',
  role_card       TEXT NOT NULL DEFAULT '{}',
  perspective     TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'idle',
  model           TEXT NOT NULL DEFAULT 'deepseek-flash',
  token_hash      TEXT NOT NULL DEFAULT '',
  budget_limit    REAL NOT NULL DEFAULT 0,
  current_task_id TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_department ON agents(department_id);
CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);

CREATE TABLE IF NOT EXISTS conversations (
  id            TEXT PRIMARY KEY,
  type          TEXT NOT NULL,
  title         TEXT NOT NULL,
  department_id TEXT NOT NULL DEFAULT '',
  visibility    TEXT NOT NULL DEFAULT 'project',
  topic         TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'active',
  expires_at    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversations_type ON conversations(type);
CREATE INDEX IF NOT EXISTS idx_conversations_dept ON conversations(department_id);

CREATE TABLE IF NOT EXISTS conversation_members (
  conversation_id TEXT NOT NULL,
  principal_type  TEXT NOT NULL,
  principal_id    TEXT NOT NULL,
  role            TEXT NOT NULL DEFAULT 'member',
  joined_at       TEXT NOT NULL,
  PRIMARY KEY (conversation_id, principal_type, principal_id)
);
CREATE INDEX IF NOT EXISTS idx_members_principal ON conversation_members(principal_type, principal_id);

CREATE TABLE IF NOT EXISTS messages (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  id              TEXT NOT NULL UNIQUE,
  conversation_id TEXT NOT NULL,
  thread_id       TEXT NOT NULL DEFAULT '',
  sender_type     TEXT NOT NULL,
  sender_id       TEXT NOT NULL,
  sender_name     TEXT NOT NULL,
  type            TEXT NOT NULL DEFAULT 'text',
  content         TEXT NOT NULL DEFAULT '',
  meta            TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL,
  edited_at       TEXT NOT NULL DEFAULT '',
  deleted_at      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON messages(conversation_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id);

CREATE TABLE IF NOT EXISTS events (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  type            TEXT NOT NULL,
  conversation_id TEXT NOT NULL DEFAULT '',
  object_type     TEXT NOT NULL DEFAULT '',
  object_id       TEXT NOT NULL DEFAULT '',
  actor_type      TEXT NOT NULL DEFAULT '',
  actor_id        TEXT NOT NULL DEFAULT '',
  payload         TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_conv_seq ON events(conversation_id, seq);
CREATE INDEX IF NOT EXISTS idx_events_object ON events(object_type, object_id);

CREATE TABLE IF NOT EXISTS search_docs (
  doc_id          TEXT PRIMARY KEY,
  kind            TEXT NOT NULL,
  ref_id          TEXT NOT NULL,
  conversation_id TEXT NOT NULL DEFAULT '',
  department_id   TEXT NOT NULL DEFAULT '',
  title           TEXT NOT NULL DEFAULT '',
  body            TEXT NOT NULL DEFAULT '',
  visibility      TEXT NOT NULL DEFAULT 'project',
  created_at      TEXT NOT NULL,
  deleted         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_search_docs_kind ON search_docs(kind);
CREATE INDEX IF NOT EXISTS idx_search_docs_conv ON search_docs(conversation_id);

CREATE TABLE IF NOT EXISTS search_terms (
  term   TEXT NOT NULL,
  doc_id TEXT NOT NULL,
  weight REAL NOT NULL DEFAULT 1,
  PRIMARY KEY (term, doc_id)
);
CREATE INDEX IF NOT EXISTS idx_search_terms_term ON search_terms(term);

CREATE TABLE IF NOT EXISTS intents (
  id         TEXT PRIMARY KEY,
  version    INTEGER NOT NULL DEFAULT 1,
  content    TEXT NOT NULL DEFAULT '{}',
  status     TEXT NOT NULL DEFAULT 'draft',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS blueprints (
  id          TEXT PRIMARY KEY,
  intent_id   TEXT NOT NULL DEFAULT '',
  content     TEXT NOT NULL DEFAULT '{}',
  status      TEXT NOT NULL DEFAULT 'proposed',
  created_at  TEXT NOT NULL,
  approved_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS provisioning (
  id           TEXT PRIMARY KEY,
  blueprint_id TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'planned',
  steps        TEXT NOT NULL DEFAULT '[]',
  error        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id                TEXT PRIMARY KEY,
  title             TEXT NOT NULL,
  description       TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'proposed',
  department_id     TEXT NOT NULL,
  owner_id          TEXT NOT NULL DEFAULT '',
  reviewer_id       TEXT NOT NULL DEFAULT '',
  approver_id       TEXT NOT NULL DEFAULT '',
  perspective       TEXT NOT NULL DEFAULT '',
  priority          TEXT NOT NULL DEFAULT 'P1',
  budget_tokens     INTEGER NOT NULL DEFAULT 0,
  budget_cost       REAL NOT NULL DEFAULT 0,
  deadline          TEXT NOT NULL DEFAULT '',
  parent_id         TEXT NOT NULL DEFAULT '',
  source_message_id TEXT NOT NULL DEFAULT '',
  dor               TEXT NOT NULL DEFAULT '{}',
  dod               TEXT NOT NULL DEFAULT '{}',
  flags             TEXT NOT NULL DEFAULT '{}',
  created_by        TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_department ON tasks(department_id);
CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks(owner_id);

CREATE TABLE IF NOT EXISTS task_criteria (
  id                TEXT PRIMARY KEY,
  task_id           TEXT NOT NULL,
  ord               INTEGER NOT NULL,
  text              TEXT NOT NULL,
  evidence          TEXT NOT NULL DEFAULT '[]',
  supervisor_verdict TEXT NOT NULL DEFAULT 'pending',
  human_decision    TEXT NOT NULL DEFAULT 'pending',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_criteria_task ON task_criteria(task_id, ord);

CREATE TABLE IF NOT EXISTS task_dependencies (
  task_id   TEXT NOT NULL,
  depends_on TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (task_id, depends_on)
);

CREATE TABLE IF NOT EXISTS task_events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id     TEXT NOT NULL,
  type        TEXT NOT NULL,
  from_status TEXT NOT NULL DEFAULT '',
  to_status   TEXT NOT NULL DEFAULT '',
  actor_type  TEXT NOT NULL,
  actor_id    TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  evidence    TEXT NOT NULL DEFAULT '[]',
  prev_hash   TEXT NOT NULL DEFAULT '',
  hash        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id, seq);

CREATE TABLE IF NOT EXISTS artifacts (
  id            TEXT PRIMARY KEY,
  type          TEXT NOT NULL,
  title         TEXT NOT NULL,
  task_id       TEXT NOT NULL DEFAULT '',
  department_id TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'draft',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_dept ON artifacts(department_id);

CREATE TABLE IF NOT EXISTS artifact_versions (
  id           TEXT PRIMARY KEY,
  artifact_id  TEXT NOT NULL,
  version      INTEGER NOT NULL,
  content      TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  storage_path TEXT NOT NULL DEFAULT '',
  author_id    TEXT NOT NULL,
  supersedes   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  UNIQUE (artifact_id, version)
);
CREATE INDEX IF NOT EXISTS idx_artifact_versions_artifact ON artifact_versions(artifact_id, version);
CREATE INDEX IF NOT EXISTS idx_artifact_versions_hash ON artifact_versions(content_hash);

CREATE TABLE IF NOT EXISTS reviews (
  id                    TEXT PRIMARY KEY,
  from_department       TEXT NOT NULL DEFAULT '',
  to_department         TEXT NOT NULL DEFAULT '',
  artifact_id           TEXT NOT NULL DEFAULT '',
  artifact_version_id   TEXT NOT NULL DEFAULT '',
  severity              TEXT NOT NULL DEFAULT 'question',
  stance                TEXT NOT NULL DEFAULT '',
  claim                 TEXT NOT NULL,
  evidence              TEXT NOT NULL DEFAULT '[]',
  impact                TEXT NOT NULL DEFAULT '',
  proposed_resolution   TEXT NOT NULL DEFAULT '',
  owner_id              TEXT NOT NULL DEFAULT '',
  due_at                TEXT NOT NULL DEFAULT '',
  status                TEXT NOT NULL DEFAULT 'open',
  thread_conversation_id TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reviews_status ON reviews(status);
CREATE INDEX IF NOT EXISTS idx_reviews_artifact ON reviews(artifact_id);

CREATE TABLE IF NOT EXISTS review_verdicts (
  id            TEXT PRIMARY KEY,
  review_id     TEXT NOT NULL,
  verdict       TEXT NOT NULL,
  checked       TEXT NOT NULL DEFAULT '',
  rationale     TEXT NOT NULL DEFAULT '',
  conditions    TEXT NOT NULL DEFAULT '[]',
  residual_risk TEXT NOT NULL DEFAULT '',
  reviewer_id   TEXT NOT NULL,
  decision_id   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_verdicts_review ON review_verdicts(review_id);

CREATE TABLE IF NOT EXISTS decisions (
  id                 TEXT PRIMARY KEY,
  title              TEXT NOT NULL,
  status             TEXT NOT NULL DEFAULT 'proposed',
  context            TEXT NOT NULL DEFAULT '',
  options            TEXT NOT NULL DEFAULT '[]',
  decision           TEXT NOT NULL DEFAULT '',
  rationale          TEXT NOT NULL DEFAULT '',
  evidence           TEXT NOT NULL DEFAULT '[]',
  dissent            TEXT NOT NULL DEFAULT '[]',
  deciders           TEXT NOT NULL DEFAULT '[]',
  consulted          TEXT NOT NULL DEFAULT '[]',
  informed           TEXT NOT NULL DEFAULT '[]',
  affected_tasks     TEXT NOT NULL DEFAULT '[]',
  affected_artifacts TEXT NOT NULL DEFAULT '[]',
  revisit_trigger    TEXT NOT NULL DEFAULT '',
  supersedes         TEXT NOT NULL DEFAULT '',
  superseded_by      TEXT NOT NULL DEFAULT '',
  human_ack          TEXT NOT NULL DEFAULT '',
  created_by         TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status);

CREATE TABLE IF NOT EXISTS approvals (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL,
  title           TEXT NOT NULL,
  payload         TEXT NOT NULL DEFAULT '{}',
  risk            TEXT NOT NULL DEFAULT 'medium',
  status          TEXT NOT NULL DEFAULT 'pending',
  requested_by    TEXT NOT NULL DEFAULT '',
  decided_by      TEXT NOT NULL DEFAULT '',
  decision_reason TEXT NOT NULL DEFAULT '',
  scope           TEXT NOT NULL DEFAULT 'once',
  expires_at      TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  decided_at      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status, kind);

CREATE TABLE IF NOT EXISTS memories (
  id            TEXT PRIMARY KEY,
  scope         TEXT NOT NULL,
  department_id TEXT NOT NULL DEFAULT '',
  agent_id      TEXT NOT NULL DEFAULT '',
  entity_type   TEXT NOT NULL DEFAULT '',
  entity_key    TEXT NOT NULL DEFAULT '',
  content       TEXT NOT NULL,
  summary       TEXT NOT NULL DEFAULT '',
  tags          TEXT NOT NULL DEFAULT '',
  confidence    TEXT NOT NULL DEFAULT 'medium',
  evidence      TEXT NOT NULL DEFAULT '[]',
  sensitivity   TEXT NOT NULL DEFAULT 'normal',
  visibility    TEXT NOT NULL DEFAULT 'team',
  valid_from    TEXT NOT NULL DEFAULT '',
  valid_to      TEXT NOT NULL DEFAULT '',
  supersedes    TEXT NOT NULL DEFAULT '',
  version       INTEGER NOT NULL DEFAULT 1,
  status        TEXT NOT NULL DEFAULT 'candidate',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  deleted_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope, department_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_memories_status ON memories(status);

CREATE TABLE IF NOT EXISTS memory_events (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id  TEXT NOT NULL,
  action     TEXT NOT NULL,
  actor_type TEXT NOT NULL DEFAULT '',
  actor_id   TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_events_memory ON memory_events(memory_id, seq);

CREATE TABLE IF NOT EXISTS prompt_manuals (
  id            TEXT PRIMARY KEY,
  scope         TEXT NOT NULL,
  department_id TEXT NOT NULL DEFAULT '',
  agent_id      TEXT NOT NULL DEFAULT '',
  version       INTEGER NOT NULL,
  content       TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'proposed',
  diff          TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  UNIQUE (scope, department_id, agent_id, version)
);
CREATE INDEX IF NOT EXISTS idx_manuals_scope ON prompt_manuals(scope, department_id, agent_id, version);

CREATE TABLE IF NOT EXISTS memory_reads (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id   TEXT NOT NULL,
  reader_type TEXT NOT NULL,
  reader_id   TEXT NOT NULL,
  turn_id     TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_reads_memory ON memory_reads(memory_id);

CREATE TABLE IF NOT EXISTS write_events (
  id         TEXT PRIMARY KEY,
  agent_id   TEXT NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  path       TEXT NOT NULL,
  base_hash  TEXT NOT NULL DEFAULT '',
  content    TEXT NOT NULL DEFAULT '',
  diff       TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL DEFAULT '',
  branch     TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'pending',
  error      TEXT NOT NULL DEFAULT '',
  applied_at TEXT NOT NULL DEFAULT '',
  commit_sha TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_write_events_status ON write_events(status);
CREATE INDEX IF NOT EXISTS idx_write_events_path ON write_events(path, created_at);

CREATE TABLE IF NOT EXISTS file_timeline (
  id             TEXT PRIMARY KEY,
  path           TEXT NOT NULL,
  write_event_id TEXT NOT NULL,
  agent_id       TEXT NOT NULL,
  task_id        TEXT NOT NULL DEFAULT '',
  diff           TEXT NOT NULL DEFAULT '',
  reason         TEXT NOT NULL DEFAULT '',
  branch         TEXT NOT NULL DEFAULT '',
  commit_sha     TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'applied',
  created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_file_timeline_path ON file_timeline(path, created_at);

CREATE TABLE IF NOT EXISTS git_events (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  type       TEXT NOT NULL,
  branch     TEXT NOT NULL DEFAULT '',
  commit_sha TEXT NOT NULL DEFAULT '',
  message    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_git_events_created ON git_events(created_at);

CREATE TABLE IF NOT EXISTS commit_groups (
  id              TEXT PRIMARY KEY,
  commit_sha      TEXT NOT NULL,
  write_event_ids TEXT NOT NULL DEFAULT '[]',
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_type  TEXT NOT NULL,
  actor_id    TEXT NOT NULL,
  action      TEXT NOT NULL,
  object_type TEXT NOT NULL DEFAULT '',
  object_id   TEXT NOT NULL DEFAULT '',
  result      TEXT NOT NULL DEFAULT 'ok',
  detail      TEXT NOT NULL DEFAULT '{}',
  prev_hash   TEXT NOT NULL DEFAULT '',
  hash        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_object ON audit_log(object_type, object_id);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor_type, actor_id);

CREATE TABLE IF NOT EXISTS usage_ledger (
  id                TEXT PRIMARY KEY,
  agent_id          TEXT NOT NULL,
  task_id           TEXT NOT NULL DEFAULT '',
  conversation_id   TEXT NOT NULL DEFAULT '',
  model             TEXT NOT NULL,
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cached_tokens     INTEGER NOT NULL DEFAULT 0,
  cost              REAL NOT NULL DEFAULT 0,
  created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_agent ON usage_ledger(agent_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_task ON usage_ledger(task_id);

CREATE TABLE IF NOT EXISTS schedules (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL,
  due_at     TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}',
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_schedules_due ON schedules(status, due_at);

CREATE TABLE IF NOT EXISTS notifications (
  id              TEXT PRIMARY KEY,
  level           TEXT NOT NULL,
  title           TEXT NOT NULL,
  body            TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL DEFAULT '',
  object_type     TEXT NOT NULL DEFAULT '',
  object_id       TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'unread',
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status, level);

CREATE TABLE IF NOT EXISTS turns (
  id              TEXT PRIMARY KEY,
  agent_id        TEXT NOT NULL,
  conversation_id TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'running',
  error           TEXT NOT NULL DEFAULT '',
  started_at      TEXT NOT NULL,
  ended_at        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_turns_agent ON turns(agent_id, started_at);

CREATE TABLE IF NOT EXISTS context_snapshots (
  id              TEXT PRIMARY KEY,
  turn_id         TEXT NOT NULL,
  agent_id        TEXT NOT NULL,
  conversation_id TEXT NOT NULL DEFAULT '',
  payload         TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_turn ON context_snapshots(turn_id);

CREATE TABLE IF NOT EXISTS bridges (
  id         TEXT PRIMARY KEY,
  scope      TEXT NOT NULL,
  from_type  TEXT NOT NULL,
  from_id    TEXT NOT NULL,
  to_type    TEXT NOT NULL,
  to_id      TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bridges_status ON bridges(status, expires_at);
