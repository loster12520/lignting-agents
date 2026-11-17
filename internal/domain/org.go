package domain

// 组织/蓝图/Provisioning 的共享类型（R3）。
// schema 已有 intents/blueprints/provisioning 表；这里只补 Go 侧契约。

const (
	IntentDraft     = "draft"
	IntentConfirmed = "confirmed"
	IntentArchived  = "archived"

	BlueprintDraft      = "draft"
	BlueprintProposed   = "proposed"
	BlueprintApproved   = "approved"
	BlueprintRejected   = "rejected"
	BlueprintSuperseded = "superseded"

	ProvisionPlanned         = "planned"
	ProvisionPlatformCreated = "platform_created"
	ProvisionCoreCreated     = "core_created"
	ProvisionVerified        = "verified"
	ProvisionRolledBack      = "rolled_back"
	ProvisionFailed          = "failed"
)

type Intent struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type Blueprint struct {
	ID         string `json:"id"`
	IntentID   string `json:"intent_id"`
	Content    string `json:"content"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ApprovedAt string `json:"approved_at"`
}

type Provisioning struct {
	ID          string `json:"id"`
	BlueprintID string `json:"blueprint_id"`
	Status      string `json:"status"`
	Steps       string `json:"steps"`
	Error       string `json:"error"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}
