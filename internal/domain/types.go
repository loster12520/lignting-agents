package domain

// Timestamp 使用 RFC3339 UTC 字符串，便于跨 SQLite/JSON/前端一致。
type Timestamp = string

const (
	AgentKindAssistant  = "assistant"
	AgentKindSupervisor = "supervisor"
	AgentKindEmployee   = "employee"

	AgentIdle          = "idle"
	AgentRunning       = "running"
	AgentWaiting       = "waiting"
	AgentWaitingReview = "waiting_review"
	AgentBudgetBlocked = "budget_blocked"
	AgentPaused        = "paused"
	AgentFailed        = "failed"
	AgentOffline       = "offline"

	DeptActive   = "active"
	DeptPaused   = "paused"
	DeptArchived = "archived"

	ConvDM             = "dm"
	ConvProject        = "project"
	ConvDepartment     = "department"
	ConvCrossDept      = "cross_dept"
	ConvTemp           = "temp"
	ConvTaskThread     = "task_thread"
	ConvArtifactThread = "artifact_thread"
	ConvDecisionLog    = "decision_log"

	SenderUser   = "user"
	SenderAgent  = "agent"
	SenderSystem = "system"
	SenderTool   = "tool"

	MsgText        = "text"
	MsgCard        = "card"
	MsgToolSummary = "tool_summary"
	MsgTaskEvent   = "task_event"
	MsgReview      = "review"
	MsgDecision    = "decision"
	MsgApproval    = "approval"
	MsgArtifact    = "artifact"
	MsgGroupEvent  = "group_event"
	MsgWriteEvent  = "write_event"
	MsgSystem      = "system"
	MsgTrace       = "trace"

	VisibilityPrivate    = "private"
	VisibilityDepartment = "department"
	VisibilityProject    = "project"
	VisibilityOrg        = "org"
	VisibilityHumanOnly  = "human_only"

	SeverityBlocker  = "blocker"
	SeverityMajor    = "major"
	SeverityMinor    = "minor"
	SeverityQuestion = "question"

	VerdictApprove        = "approve"
	VerdictApproveComment = "approve_with_comments"
	VerdictRequestChanges = "request_changes"
	VerdictEscalate       = "escalate"

	DecisionProposed   = "proposed"
	DecisionAccepted   = "accepted"
	DecisionSuperseded = "superseded"
	DecisionRejected   = "rejected"

	ApprovalG1           = "G1"
	ApprovalG2           = "G2"
	ApprovalG3           = "G3"
	ApprovalIrreversible = "irreversible"
	ApprovalPending      = "pending"
	ApprovalApproved     = "approved"
	ApprovalRejected     = "rejected"
	ApprovalExpired      = "expired"
	ApprovalBlocked      = "blocked"

	MemoryScopeAssistant  = "assistant"
	MemoryScopeDepartment = "department"
	MemoryScopeEmployee   = "employee"

	MemoryStatusCandidate = "candidate"
	MemoryStatusActive    = "active"
	MemoryStatusRejected  = "rejected"
	MemoryStatusDeleted   = "deleted"

	MemoryAgentPrivate = "agent-private"
	MemoryTeam         = "team"
	MemoryOrg          = "org"
	MemoryHumanOnly    = "human-only"

	WritePending  = "pending"
	WriteApplied  = "applied"
	WriteRejected = "rejected"
	WriteUnknown  = "unknown"

	NotifyFYI        = "fyi"
	NotifyNeedKnow   = "need_know"
	NotifyNeedDecide = "need_decide"
	NotifyBlocking   = "blocking"

	BridgeCrossDept  = "cross_dept"
	BridgeLeadToUser = "lead_to_user"
	BridgeAgentPair  = "agent_pair"
)

type Agent struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	DepartmentID  string    `json:"department_id"`
	RoleCard      string    `json:"role_card"`
	Perspective   string    `json:"perspective"`
	Status        string    `json:"status"`
	Model         string    `json:"model"`
	TokenHash     string    `json:"-"`
	BudgetLimit   float64   `json:"budget_limit"`
	CurrentTaskID string    `json:"current_task_id"`
	CreatedAt     Timestamp `json:"created_at"`
	UpdatedAt     Timestamp `json:"updated_at"`
}

type Department struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Mission   string    `json:"mission"`
	Status    string    `json:"status"`
	Charter   string    `json:"charter"`
	CreatedAt Timestamp `json:"created_at"`
	UpdatedAt Timestamp `json:"updated_at"`
}

type Conversation struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	DepartmentID string    `json:"department_id"`
	Visibility   string    `json:"visibility"`
	Topic        string    `json:"topic"`
	CreatedBy    string    `json:"created_by"`
	Status       string    `json:"status"`
	ExpiresAt    string    `json:"expires_at"`
	CreatedAt    Timestamp `json:"created_at"`
	UpdatedAt    Timestamp `json:"updated_at"`
}

type ConversationMember struct {
	ConversationID string    `json:"conversation_id"`
	PrincipalType  string    `json:"principal_type"`
	PrincipalID    string    `json:"principal_id"`
	Role           string    `json:"role"`
	JoinedAt       Timestamp `json:"joined_at"`
}

type Message struct {
	Seq            int64     `json:"seq"`
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	ThreadID       string    `json:"thread_id"`
	SenderType     string    `json:"sender_type"`
	SenderID       string    `json:"sender_id"`
	SenderName     string    `json:"sender_name"`
	Type           string    `json:"type"`
	Content        string    `json:"content"`
	Meta           string    `json:"meta"`
	CreatedAt      Timestamp `json:"created_at"`
	EditedAt       string    `json:"edited_at"`
	DeletedAt      string    `json:"deleted_at"`
}

type Event struct {
	Seq            int64     `json:"seq"`
	Type           string    `json:"type"`
	ConversationID string    `json:"conversation_id"`
	ObjectType     string    `json:"object_type"`
	ObjectID       string    `json:"object_id"`
	ActorType      string    `json:"actor_type"`
	ActorID        string    `json:"actor_id"`
	Payload        string    `json:"payload"`
	CreatedAt      Timestamp `json:"created_at"`
}

type Task struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	DepartmentID    string    `json:"department_id"`
	OwnerID         string    `json:"owner_id"`
	ReviewerID      string    `json:"reviewer_id"`
	ApproverID      string    `json:"approver_id"`
	Perspective     string    `json:"perspective"`
	Priority        string    `json:"priority"`
	BudgetTokens    int64     `json:"budget_tokens"`
	BudgetCost      float64   `json:"budget_cost"`
	Deadline        string    `json:"deadline"`
	ParentID        string    `json:"parent_id"`
	SourceMessageID string    `json:"source_message_id"`
	DoR             string    `json:"dor"`
	DoD             string    `json:"dod"`
	Flags           string    `json:"flags"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       Timestamp `json:"created_at"`
	UpdatedAt       Timestamp `json:"updated_at"`
}

type Criterion struct {
	ID                string `json:"id"`
	TaskID            string `json:"task_id"`
	Ord               int    `json:"ord"`
	Text              string `json:"text"`
	Evidence          string `json:"evidence"`
	SupervisorVerdict string `json:"supervisor_verdict"`
	HumanDecision     string `json:"human_decision"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type TaskEvent struct {
	Seq        int64     `json:"seq"`
	TaskID     string    `json:"task_id"`
	Type       string    `json:"type"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	ActorType  string    `json:"actor_type"`
	ActorID    string    `json:"actor_id"`
	Reason     string    `json:"reason"`
	Evidence   string    `json:"evidence"`
	PrevHash   string    `json:"prev_hash"`
	Hash       string    `json:"hash"`
	CreatedAt  Timestamp `json:"created_at"`
}

type Artifact struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	TaskID       string    `json:"task_id"`
	DepartmentID string    `json:"department_id"`
	Status       string    `json:"status"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    Timestamp `json:"created_at"`
}

type ArtifactVersion struct {
	ID          string    `json:"id"`
	ArtifactID  string    `json:"artifact_id"`
	Version     int       `json:"version"`
	Content     string    `json:"content"`
	ContentHash string    `json:"content_hash"`
	StoragePath string    `json:"storage_path"`
	AuthorID    string    `json:"author_id"`
	Supersedes  string    `json:"supersedes"`
	CreatedAt   Timestamp `json:"created_at"`
}

type Review struct {
	ID                   string    `json:"id"`
	FromDepartment       string    `json:"from_department"`
	ToDepartment         string    `json:"to_department"`
	ArtifactID           string    `json:"artifact_id"`
	ArtifactVersionID    string    `json:"artifact_version_id"`
	Severity             string    `json:"severity"`
	Stance               string    `json:"stance"`
	Claim                string    `json:"claim"`
	Evidence             string    `json:"evidence"`
	Impact               string    `json:"impact"`
	ProposedResolution   string    `json:"proposed_resolution"`
	OwnerID              string    `json:"owner_id"`
	DueAt                string    `json:"due_at"`
	Status               string    `json:"status"`
	ThreadConversationID string    `json:"thread_conversation_id"`
	CreatedAt            Timestamp `json:"created_at"`
}

type ReviewVerdict struct {
	ID           string    `json:"id"`
	ReviewID     string    `json:"review_id"`
	Verdict      string    `json:"verdict"`
	Checked      string    `json:"checked"`
	Rationale    string    `json:"rationale"`
	Conditions   string    `json:"conditions"`
	ResidualRisk string    `json:"residual_risk"`
	ReviewerID   string    `json:"reviewer_id"`
	DecisionID   string    `json:"decision_id"`
	CreatedAt    Timestamp `json:"created_at"`
}

type Decision struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	Status            string    `json:"status"`
	Context           string    `json:"context"`
	Options           string    `json:"options"`
	Decision          string    `json:"decision"`
	Rationale         string    `json:"rationale"`
	Evidence          string    `json:"evidence"`
	Dissent           string    `json:"dissent"`
	Deciders          string    `json:"deciders"`
	Consulted         string    `json:"consulted"`
	Informed          string    `json:"informed"`
	AffectedTasks     string    `json:"affected_tasks"`
	AffectedArtifacts string    `json:"affected_artifacts"`
	RevisitTrigger    string    `json:"revisit_trigger"`
	Supersedes        string    `json:"supersedes"`
	SupersededBy      string    `json:"superseded_by"`
	HumanAck          string    `json:"human_ack"`
	CreatedBy         string    `json:"created_by"`
	CreatedAt         Timestamp `json:"created_at"`
}

type Approval struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Payload        string    `json:"payload"`
	Risk           string    `json:"risk"`
	Status         string    `json:"status"`
	RequestedBy    string    `json:"requested_by"`
	DecidedBy      string    `json:"decided_by"`
	DecisionReason string    `json:"decision_reason"`
	Scope          string    `json:"scope"`
	ExpiresAt      string    `json:"expires_at"`
	CreatedAt      Timestamp `json:"created_at"`
	DecidedAt      string    `json:"decided_at"`
}

type Memory struct {
	ID           string    `json:"id"`
	Scope        string    `json:"scope"`
	DepartmentID string    `json:"department_id"`
	AgentID      string    `json:"agent_id"`
	EntityType   string    `json:"entity_type"`
	EntityKey    string    `json:"entity_key"`
	Content      string    `json:"content"`
	Summary      string    `json:"summary"`
	Tags         string    `json:"tags"`
	Confidence   string    `json:"confidence"`
	Evidence     string    `json:"evidence"`
	Sensitivity  string    `json:"sensitivity"`
	Visibility   string    `json:"visibility"`
	ValidFrom    string    `json:"valid_from"`
	ValidTo      string    `json:"valid_to"`
	Supersedes   string    `json:"supersedes"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    Timestamp `json:"created_at"`
	UpdatedAt    Timestamp `json:"updated_at"`
	DeletedAt    string    `json:"deleted_at"`
}

type PromptManual struct {
	ID           string    `json:"id"`
	Scope        string    `json:"scope"`
	DepartmentID string    `json:"department_id"`
	AgentID      string    `json:"agent_id"`
	Version      int       `json:"version"`
	Content      string    `json:"content"`
	Status       string    `json:"status"`
	Diff         string    `json:"diff"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    Timestamp `json:"created_at"`
}

type WriteEvent struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	TaskID    string    `json:"task_id"`
	Path      string    `json:"path"`
	BaseHash  string    `json:"base_hash"`
	Content   string    `json:"content,omitempty"`
	Diff      string    `json:"diff"`
	Reason    string    `json:"reason"`
	Branch    string    `json:"branch"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	AppliedAt string    `json:"applied_at"`
	CommitSHA string    `json:"commit_sha"`
	CreatedAt Timestamp `json:"created_at"`
}

type FileTimelineEntry struct {
	ID           string    `json:"id"`
	Path         string    `json:"path"`
	WriteEventID string    `json:"write_event_id"`
	AgentID      string    `json:"agent_id"`
	TaskID       string    `json:"task_id"`
	Diff         string    `json:"diff"`
	Reason       string    `json:"reason"`
	Branch       string    `json:"branch"`
	CommitSHA    string    `json:"commit_sha"`
	Status       string    `json:"status"`
	CreatedAt    Timestamp `json:"created_at"`
}

type AuditEntry struct {
	Seq        int64     `json:"seq"`
	ActorType  string    `json:"actor_type"`
	ActorID    string    `json:"actor_id"`
	Action     string    `json:"action"`
	ObjectType string    `json:"object_type"`
	ObjectID   string    `json:"object_id"`
	Result     string    `json:"result"`
	Detail     string    `json:"detail"`
	PrevHash   string    `json:"prev_hash"`
	Hash       string    `json:"hash"`
	CreatedAt  Timestamp `json:"created_at"`
}

type Usage struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	TaskID           string    `json:"task_id"`
	ConversationID   string    `json:"conversation_id"`
	Model            string    `json:"model"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CachedTokens     int64     `json:"cached_tokens"`
	Cost             float64   `json:"cost"`
	CreatedAt        Timestamp `json:"created_at"`
}

type ContextSnapshot struct {
	ID             string    `json:"id"`
	TurnID         string    `json:"turn_id"`
	AgentID        string    `json:"agent_id"`
	ConversationID string    `json:"conversation_id"`
	Payload        string    `json:"payload"`
	CreatedAt      Timestamp `json:"created_at"`
}

type Turn struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	StartedAt      string `json:"started_at"`
	EndedAt        string `json:"ended_at"`
}

type Bridge struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	FromType  string    `json:"from_type"`
	FromID    string    `json:"from_id"`
	ToType    string    `json:"to_type"`
	ToID      string    `json:"to_id"`
	ExpiresAt string    `json:"expires_at"`
	Status    string    `json:"status"`
	CreatedAt Timestamp `json:"created_at"`
}
