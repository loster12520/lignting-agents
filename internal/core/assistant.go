package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

// ClarifyQuestion 是澄清阶段的一条结构化问题（FR-ASST-01）。
type ClarifyQuestion struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Examples []string `json:"examples,omitempty"`
}

// FastClarifyQuestions 是「快速模式」的 3 个必答问题：目标、必须有的功能、预算档位。
func FastClarifyQuestions() []ClarifyQuestion {
	return []ClarifyQuestion{
		{ID: "goal", Text: "你想做成什么？一句话说清目标与目标用户。", Examples: []string{"给内部团队做一个知识库", "做一个本地记账小工具"}},
		{ID: "must_have", Text: "必须有的功能有哪些？（3–5 条即可，先不追求完整）", Examples: []string{"登录", "搜索", "导出"}},
		{ID: "budget", Text: "预算档位与期限？", Examples: []string{"每天 10 元以内 / 两周", "每月 100 元以内 / 一个月"}},
	}
}

// FullInterviewQuestions 是完整访谈提纲（用户可随时跳过，默认值后续补全）。
func FullInterviewQuestions() []ClarifyQuestion {
	return []ClarifyQuestion{
		{ID: "goal", Text: "目标是什么？成功时用户能完成哪件事？"},
		{ID: "users", Text: "目标用户是谁？他们的使用场景是什么？"},
		{ID: "scope", Text: "范围边界：必须做、明确不做分别是什么？"},
		{ID: "constraints", Text: "约束：技术栈、部署环境、合规/隐私要求？"},
		{ID: "quality", Text: "质量要求：性能、可靠性、可用性到什么程度？"},
		{ID: "budget", Text: "预算档位与成本上限？"},
		{ID: "deadline", Text: "期限与里程碑？"},
		{ID: "risks", Text: "你最担心的风险是什么？"},
		{ID: "assets", Text: "现有资产：代码、文档、数据、账号？"},
	}
}

// IntentCard 是项目意图卡（FR-ASST-01）。版本由 store 递增。
type IntentCard struct {
	Goal            string   `json:"goal"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
	MustHave        []string `json:"must_have,omitempty"`
	MustNot         []string `json:"must_not,omitempty"`
	Constraints     []string `json:"constraints,omitempty"`
	Budget          string   `json:"budget,omitempty"`
	Deadline        string   `json:"deadline,omitempty"`
	Risks           []string `json:"risks,omitempty"`
	OpenQuestions   []string `json:"open_questions,omitempty"`
}

// NewIntentCard 从快速模式答案生成意图卡；未提供的项进入 OpenQuestions。
func NewIntentCard(goal string, mustHave []string, budget string) IntentCard {
	card := IntentCard{Goal: strings.TrimSpace(goal), MustHave: compact(mustHave), Budget: strings.TrimSpace(budget)}
	if card.Goal == "" {
		card.OpenQuestions = append(card.OpenQuestions, "目标尚未确认")
	}
	if len(card.MustHave) == 0 {
		card.OpenQuestions = append(card.OpenQuestions, "必须有的功能尚未确认")
	}
	if card.Budget == "" {
		card.OpenQuestions = append(card.OpenQuestions, "预算档位尚未确认")
	}
	return card
}

// JSON 返回意图卡 JSON（写入 intent 表的内容）。
func (c IntentCard) JSON() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Action 构造 save_intent 动作（需要用户确认后才由模型发出）。
func (c IntentCard) Action() (domain.Action, error) {
	content, err := c.JSON()
	if err != nil {
		return domain.Action{}, err
	}
	args, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return domain.Action{}, err
	}
	return domain.Action{Action: "save_intent", Args: args}, nil
}

// Render 渲染为给用户看的一页意图卡。
func (c IntentCard) Render() string {
	var b strings.Builder
	b.WriteString("项目意图卡\n")
	fmt.Fprintf(&b, "目标：%s\n", fallback(c.Goal, "（待确认）"))
	writeList(&b, "成功标准", c.SuccessCriteria)
	writeList(&b, "必须做", c.MustHave)
	writeList(&b, "明确不做", c.MustNot)
	writeList(&b, "约束", c.Constraints)
	if c.Budget != "" {
		fmt.Fprintf(&b, "预算：%s\n", c.Budget)
	}
	if c.Deadline != "" {
		fmt.Fprintf(&b, "期限：%s\n", c.Deadline)
	}
	writeList(&b, "风险", c.Risks)
	writeList(&b, "待确认", c.OpenQuestions)
	return strings.TrimSpace(b.String())
}

func writeList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "%s：\n", title)
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
}

func compact(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// AssistantGreeting 是助理首启的确定性自我介绍（不依赖模型调用，mock/无 Key 都可用）。
func AssistantGreeting(assistantName, ownerName string) string {
	assistant := fallback(assistantName, "助理")
	owner := fallback(ownerName, "项目主管")
	return strings.Join([]string{
		"你好，" + owner + "。我是 " + assistant + "，你的项目助理。",
		"",
		"我可以帮你：",
		"1. 把模糊想法澄清成可执行目标，并维护一页「项目意图卡」；",
		"2. 提出项目/部门蓝图，等你批准 G1 后再组建产品/研发/测试部门；",
		"3. 协调部门主管、整理跨部门争议与决策包；",
		"4. 按需汇总进展、阻塞、风险与成本（不转发所有消息）；",
		"5. 管理记忆：记住你的偏好与项目决策，并接受你的纠正。",
		"",
		"我不会：替你批准预算/章程、修改自己的权限、删除审计、冒充你说话，也不会在你确认前创建部门。",
		"",
		"你可以随时暂停我或停止当前回合；需要我先做哪件事？如果还没想好，直接说「我想做 X」就行。",
	}, "\n")
}

// DegradedMessage 把模型错误翻译成用户可读、可重试的提示（不泄露 Key/堆栈）。
func DegradedMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrEmergencyStop):
		return "系统正在紧急停止中，本轮已中止。你可以先浏览/检索；恢复后重试即可，不会丢消息。"
	case isBudgetBlocked(err):
		return err.Error()
	case deepseek.IsNoAPIKey(err):
		return strings.Join([]string{
			"我现在无法调用模型：还没有配置 DeepSeek API Key。",
			"你可以：在「设置 → 模型」填入 Key 后重试；在此之前我仍可以帮你整理需求草稿、浏览历史会话与检索。",
			"本轮已记录为失败并可重试，不会丢消息。",
		}, "\n")
	case deepseek.IsRateLimited(err):
		return "模型暂时被限流了（429）。稍等一会儿再重试；你可以先继续浏览或检索，不需要重新组织需求。"
	case deepseek.IsContextWindowExceeded(err):
		return "上下文超出模型窗口了。请缩小本轮范围，或让我先做一次摘要压缩后再继续。"
	default:
		return "模型调用失败：" + truncateRunes(err.Error(), 300) + "\n已记录到审计；你可以稍后重试本轮。"
	}
}
