package memory

import (
	"fmt"
	"strings"
)

// OverflowAction 决定档案超过存储上限时的行为（FR-MEM-06）。
const (
	// OverflowSummarize 先完整落库，再进入待压缩队列（默认）。
	OverflowSummarize = "summarize"
	// OverflowReject 拒绝写入并给出压缩指引。
	OverflowReject = "reject"
)

// ManualMode 决定管理手册新版本的默认审批行为（FR-MEM-07）。
const (
	// ManualProposeOnly v0.0.1 默认：所有新版本都必须人工批准。
	ManualProposeOnly = "propose_only"
	// ManualAutoLowRisk 低风险、非红线章节的小改动可自动生效（需显式开启）。
	ManualAutoLowRisk = "auto_low_risk"
)

// 默认值集中在此，测试与文档引用同一份真相。
const (
	defaultCandidateMaxPerTask   = 5
	defaultCandidateMaxChars     = 200
	defaultCandidateMaxPerDay    = 100
	defaultCandidateMaxPerAgent  = 200
	defaultStorageMaxChars       = 15000
	defaultRenderSummaryMaxChars = 300
	defaultRenderContentMaxChars = 800
	defaultCompressTargetChars   = 10000
	defaultCompressCooldownSec   = 600
	defaultCompressMaxPending    = 50
	defaultInjectPercent         = 0.25
	defaultInjectAbsCapTokens    = 6000
	defaultSafetyMarginPermille  = 80
	defaultManualMaxChars        = 1500
	defaultManualAutoMaxDiff     = 8
)

// Config 是记忆模块的可调参数。零值可用，NewService 会套用 DefaultConfig 再归一化。
type Config struct {
	Enabled bool

	// 候选写入额度（FR-MEM-02/04）。
	CandidateMaxPerTask  int
	CandidateMaxChars    int
	CandidateMaxPerDay   int
	CandidateMaxPerAgent int
	RequireEvidence      bool
	AllowLowConfidence   bool

	// 容量治理（FR-MEM-06）：渲染上限与存储上限是两件独立的事。
	StorageMaxChars        int
	RenderSummaryMaxChars  int
	RenderContentMaxChars  int
	OverflowAction         string
	CompressTargetChars    int
	CompressCooldownSecond int
	CompressMaxPending     int
	ExemptEntityTypes      []string

	// 注入预算（FR-MEM-08）。
	InjectPercent         float64 // 占有效输入预算比例。
	InjectAbsCapTokens    int64   // 绝对上限。
	SafetyMarginPeremille int     // 保留 5–10% 安全边际（千分比）。
	PartitionManual       float64
	PartitionTask         float64
	PartitionArchive      float64
	PartitionRecent       float64

	// 管理手册（FR-MEM-07）。
	ManualMaxChars    int
	ManualMode        string
	ManualAutoMaxDiff int // auto_low_risk 允许的最大变更行数。

	// 隐私与审计（FR-MEM-10）。
	RedactSensitiveForAgents bool
	ReadTraceEnabled         bool
}

// DefaultConfig 返回与 requirements 默认值对齐的配置。
func DefaultConfig() Config {
	return Config{
		Enabled: true,

		CandidateMaxPerTask:  defaultCandidateMaxPerTask,
		CandidateMaxChars:    defaultCandidateMaxChars,
		CandidateMaxPerDay:   defaultCandidateMaxPerDay,
		CandidateMaxPerAgent: defaultCandidateMaxPerAgent,
		RequireEvidence:      true,
		AllowLowConfidence:   true,

		StorageMaxChars:        defaultStorageMaxChars,
		RenderSummaryMaxChars:  defaultRenderSummaryMaxChars,
		RenderContentMaxChars:  defaultRenderContentMaxChars,
		OverflowAction:         OverflowSummarize,
		CompressTargetChars:    defaultCompressTargetChars,
		CompressCooldownSecond: defaultCompressCooldownSec,
		CompressMaxPending:     defaultCompressMaxPending,
		ExemptEntityTypes:      []string{"__internal", "memory_counter"},

		InjectPercent:         defaultInjectPercent,
		InjectAbsCapTokens:    defaultInjectAbsCapTokens,
		SafetyMarginPeremille: defaultSafetyMarginPermille,
		PartitionManual:       0.08,
		PartitionTask:         0.42,
		PartitionArchive:      0.30,
		PartitionRecent:       0.20,

		ManualMaxChars:    defaultManualMaxChars,
		ManualMode:        ManualProposeOnly,
		ManualAutoMaxDiff: defaultManualAutoMaxDiff,

		RedactSensitiveForAgents: true,
		ReadTraceEnabled:         true,
	}
}

// Normalize 把零值补成默认值，并夹到合法区间。幂等。
func (c Config) Normalize() Config {
	d := DefaultConfig()
	if c.CandidateMaxPerTask <= 0 {
		c.CandidateMaxPerTask = d.CandidateMaxPerTask
	}
	if c.CandidateMaxChars <= 0 {
		c.CandidateMaxChars = d.CandidateMaxChars
	}
	if c.CandidateMaxPerDay <= 0 {
		c.CandidateMaxPerDay = d.CandidateMaxPerDay
	}
	if c.CandidateMaxPerAgent <= 0 {
		c.CandidateMaxPerAgent = d.CandidateMaxPerAgent
	}
	if c.StorageMaxChars <= 0 {
		c.StorageMaxChars = d.StorageMaxChars
	}
	if c.RenderSummaryMaxChars <= 0 {
		c.RenderSummaryMaxChars = d.RenderSummaryMaxChars
	}
	if c.RenderContentMaxChars <= 0 {
		c.RenderContentMaxChars = d.RenderContentMaxChars
	}
	if c.CompressTargetChars <= 0 {
		c.CompressTargetChars = d.CompressTargetChars
	}
	if c.CompressTargetChars > c.StorageMaxChars {
		c.CompressTargetChars = c.StorageMaxChars
	}
	if c.CompressCooldownSecond <= 0 {
		c.CompressCooldownSecond = d.CompressCooldownSecond
	}
	if c.CompressMaxPending <= 0 {
		c.CompressMaxPending = d.CompressMaxPending
	}
	if len(c.ExemptEntityTypes) == 0 {
		c.ExemptEntityTypes = d.ExemptEntityTypes
	}
	if c.OverflowAction != OverflowReject {
		c.OverflowAction = OverflowSummarize
	}
	if c.InjectPercent <= 0 || c.InjectPercent > 1 {
		c.InjectPercent = d.InjectPercent
	}
	if c.InjectAbsCapTokens <= 0 {
		c.InjectAbsCapTokens = d.InjectAbsCapTokens
	}
	if c.SafetyMarginPeremille <= 0 || c.SafetyMarginPeremille > 300 {
		c.SafetyMarginPeremille = d.SafetyMarginPeremille
	}
	if c.ManualMaxChars <= 0 {
		c.ManualMaxChars = d.ManualMaxChars
	}
	if c.ManualMode != ManualAutoLowRisk {
		c.ManualMode = ManualProposeOnly
	}
	if c.ManualAutoMaxDiff <= 0 {
		c.ManualAutoMaxDiff = d.ManualAutoMaxDiff
	}
	// 分区比例归一化：全部为 0 时用默认；否则按和归一，允许只调一两个分区。
	sum := c.PartitionManual + c.PartitionTask + c.PartitionArchive + c.PartitionRecent
	if sum <= 0 {
		c.PartitionManual, c.PartitionTask, c.PartitionArchive, c.PartitionRecent =
			d.PartitionManual, d.PartitionTask, d.PartitionArchive, d.PartitionRecent
	} else {
		c.PartitionManual /= sum
		c.PartitionTask /= sum
		c.PartitionArchive /= sum
		c.PartitionRecent /= sum
	}
	return c
}

// SafetyMargin 返回 0–0.30 的安全边际比例。
func (c Config) SafetyMargin() float64 {
	c = c.Normalize()
	return float64(c.SafetyMarginPeremille) / 1000
}

// Validate 校验配置；错误信息面向运维。
func (c Config) Validate() error {
	if c.OverflowAction != OverflowSummarize && c.OverflowAction != OverflowReject {
		return fmt.Errorf("memory: 未知 overflow_action %q", c.OverflowAction)
	}
	if c.ManualMode != ManualProposeOnly && c.ManualMode != ManualAutoLowRisk {
		return fmt.Errorf("memory: 未知 manual_mode %q", c.ManualMode)
	}
	if c.StorageMaxChars > 0 && c.CompressTargetChars >= c.StorageMaxChars {
		return fmt.Errorf("memory: compress_target_chars(%d) 必须小于 storage_max_chars(%d)", c.CompressTargetChars, c.StorageMaxChars)
	}
	for _, e := range c.ExemptEntityTypes {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("memory: exempt_entity_types 含空值")
		}
	}
	return nil
}

// IsExemptEntity 报告某实体类型是否豁免容量治理（内部/元数据表）。
func (c Config) IsExemptEntity(entityType string) bool {
	for _, e := range c.ExemptEntityTypes {
		if e == entityType {
			return true
		}
	}
	return false
}
