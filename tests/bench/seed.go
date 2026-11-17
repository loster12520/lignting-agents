// Package bench 提供 10 万条消息检索基准的种子数据（仅测试用）。
package bench

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

// SeedStats 是种子数据统计。
type SeedStats struct {
	Messages      int
	Conversations []string
	Departments   []string
	// GoldQueries: 唯一英文 token -> 目标消息 ID（Recall@10 金标准）。
	GoldQueries map[string]string
	Terms       int64
}

var chineseNoise = []string{
	"登录接口需要限流与审计", "部署流水线今天又失败了一次", "测试覆盖率不足需要补用例",
	"返工原因是接口契约变更", "部门评审要求补充证据", "前端页面加载速度需要优化",
	"数据库迁移要在停写窗口执行", "记忆注入预算需要控制", "任务状态机不允许跳步",
	"跨部门辩论需要新证据", "评审裁决必须说明检查项", "备份恢复演练已完成",
	"知识库检索支持中文两字", "决策记录不可原地修改", "审计日志必须哈希链校验",
}
var englishNoise = []string{
	"login rate limit audit", "deploy pipeline failed again", "test coverage is low",
	"interface contract changed", "need more evidence for review", "page load too slow",
	"run database migration in window", "memory injection budget", "task state machine",
}
var codeNoise = []string{
	"getUserById(id) returns user", "internal/store/messages.go#L100", "func Login(ctx, req)",
	"SELECT seq FROM messages ORDER BY seq", "search.IndexTx(tx, doc)", "core.DecisionSuperseded",
}

// Seed 批量写入 total 条消息并同步倒排索引（与生产同一 Indexer；为基准速度省略事件/审计）。
// 返回 GoldQueries 用于 Recall@10 校验。
func Seed(ctx context.Context, st *store.Store, total int) (SeedStats, error) {
	stats := SeedStats{GoldQueries: map[string]string{}}
	idx := search.NewIndexer()
	now := store.Now()

	// 4 部门 × 5 会话
	senders := []struct{ typ, id, name string }{
		{domain.SenderUser, store.OwnerID, "阿哲"},
		{domain.SenderAgent, "agent_sup_1", "研发主管"},
		{domain.SenderAgent, "agent_emp_1", "研发员工"},
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT OR REPLACE INTO departments(id,name,kind,status,charter,created_at,updated_at) VALUES('dept_bench_1','研发部','engineering','active','{}',?,?)", now, now); err != nil {
		return stats, err
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT OR REPLACE INTO departments(id,name,kind,status,charter,created_at,updated_at) VALUES('dept_bench_2','测试部','qa','active','{}',?,?)", now, now); err != nil {
		return stats, err
	}
	stats.Departments = []string{"dept_bench_1", "dept_bench_2"}
	type convMeta struct{ id, dept, visibility string }
	convs := make([]convMeta, 0, 20)
	for d := 0; d < 4; d++ {
		dept := "dept_bench_1"
		if d%2 == 1 {
			dept = "dept_bench_2"
		}
		for c := 0; c < 5; c++ {
			id := fmt.Sprintf("conv_bench_%02d", d*5+c)
			if _, err := st.DB.ExecContext(ctx, "INSERT OR REPLACE INTO conversations(id,type,title,department_id,visibility,created_by,status,created_at,updated_at) VALUES(?,'department',?,?,'department','owner','active',?,?)",
				id, id, dept, now, now); err != nil {
				return stats, err
			}
			convs = append(convs, convMeta{id: id, dept: dept, visibility: domain.VisibilityDepartment})
			stats.Conversations = append(stats.Conversations, id)
		}
	}

	const batch = 500
	for start := 0; start < total; start += batch {
		end := start + batch
		if end > total {
			end = total
		}
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			msgStmt, err := tx.Prepare("INSERT INTO messages(id,conversation_id,thread_id,sender_type,sender_id,sender_name,type,content,meta,created_at,edited_at,deleted_at) VALUES(?,?,'',?,?,?,'text',?,'{}',?,'','')")
			if err != nil {
				return err
			}
			defer msgStmt.Close()
			for i := start; i < end; i++ {
				conv := convs[i%len(convs)]
				sender := senders[i%len(senders)]
				content := buildContent(i, total)
				msgID := fmt.Sprintf("msg_bench_%06d", i)
				if _, err := msgStmt.Exec(msgID, conv.id, sender.typ, sender.id, sender.name, content, now); err != nil {
					return err
				}
				doc := store.IndexDoc{
					DocID: store.MessageDocID(msgID), Kind: "message", RefID: msgID,
					ConversationID: conv.id, DepartmentID: conv.dept, Body: content,
					Visibility: conv.visibility, CreatedAt: now,
				}
				if err := idx.IndexTx(tx, doc); err != nil {
					return err
				}
				// 前 100 条消息作为 Recall 金标准 token（goldqueryNN 唯一词）。
				if i < 100 && i%5 == 0 {
					stats.GoldQueries[fmt.Sprintf("goldquery%02d", i/5)] = msgID
				}
			}
			return nil
		})
		if err != nil {
			return stats, err
		}
		stats.Messages = end
	}
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms").Scan(&stats.Terms); err != nil {
		return stats, err
	}
	return stats, nil
}

// buildContent 生成混合中文/英文/代码内容，并注入金标准唯一 token。
func buildContent(i, total int) string {
	var b strings.Builder
	b.WriteString(chineseNoise[i%len(chineseNoise)])
	b.WriteString(" ")
	b.WriteString(englishNoise[(i*7)%len(englishNoise)])
	b.WriteString(" ")
	b.WriteString(codeNoise[(i*11)%len(codeNoise)])
	b.WriteString(fmt.Sprintf(" 序号%d", i))
	// 10% 含中文两字目标词；5% 含代码标识符；20% 含 login。
	if i%10 == 0 {
		b.WriteString(" 限流策略")
	}
	if i%20 == 0 {
		b.WriteString(" getUserById")
	}
	if i%5 == 0 {
		b.WriteString(" login")
	}
	if i < 100 && i%5 == 0 {
		b.WriteString(fmt.Sprintf(" goldquery%02d", i/5))
	}
	return b.String()
}
