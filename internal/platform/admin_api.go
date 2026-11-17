package platform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/backup"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R8 运维 API：备份/导出/导入/恢复/紧急停止/审计。备份/导出实现由 internal/backup 提供。

func mapBackupError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, backup.ErrNotFound):
		return errNotFound("备份/导出文件不存在")
	case errors.Is(err, backup.ErrConfirmRequired):
		return errConflict("目标非空，导入需要 confirm=true")
	case errors.Is(err, backup.ErrTargetNotEmpty):
		return errConflict("目标目录非空或恢复窗口未就绪")
	case errors.Is(err, backup.ErrCorrupt), errors.Is(err, backup.ErrVersionMismatch),
		errors.Is(err, backup.ErrInvalidDataDir), errors.Is(err, backup.ErrInvalidExport),
		errors.Is(err, backup.ErrConflict):
		return errInvalid("备份/导出/导入数据不合法：" + err.Error())
	default:
		return err
	}
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	result, err := backup.Backup(r.Context(), s.cfg.DataDir)
	if err != nil {
		s.writeErr(w, mapBackupError(err))
		return
	}
	s.audit(r.Context(), domain.SenderUser, owner.ID, "admin.backup", "backup", filepath.Base(result.Path), "ok",
		jsonDetail(map[string]any{"path": result.Path, "size": result.Size, "sha256": result.SHA256}))
	writeOK(w, result)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	dir := filepath.Join(s.cfg.DataDir, backup.DefaultBackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		s.writeErr(w, err)
		return
	}
	type item struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Size      int64  `json:"size"`
		CreatedAt string `json:"created_at"`
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	writeList(w, items, int64(len(items)), "")
}

type restoreRequest struct {
	BackupPath string `json:"backup_path"`
	TargetDir  string `json:"target_dir"`
	Confirm    bool   `json:"confirm"`
}

// handleRestore 恢复只写新目录（明确停写窗口/二次确认），不覆盖当前打开的数据目录。
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	var req restoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if !req.Confirm {
		s.writeErr(w, errConflict("恢复必须显式 confirm=true（请在停写窗口执行）"))
		return
	}
	if strings.TrimSpace(req.BackupPath) == "" || strings.TrimSpace(req.TargetDir) == "" {
		s.writeErr(w, errInvalid("backup_path/target_dir 必填"))
		return
	}
	absTarget, err := filepath.Abs(req.TargetDir)
	if err != nil {
		s.writeErr(w, errInvalid("target_dir 不合法"))
		return
	}
	absCurrent, _ := filepath.Abs(s.cfg.DataDir)
	if absTarget == absCurrent {
		s.writeErr(w, errConflict("不能恢复到正在运行的数据目录；请指定新目录后重启"))
		return
	}
	manifest, err := backup.Restore(r.Context(), req.BackupPath, absTarget)
	if err != nil {
		s.writeErr(w, mapBackupError(err))
		return
	}
	s.audit(r.Context(), domain.SenderUser, owner.ID, "admin.restore", "backup", filepath.Base(req.BackupPath), "ok",
		jsonDetail(map[string]any{"target_dir": absTarget, "backup_id": manifest.BackupID}))
	writeOK(w, manifest)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	result, err := backup.Export(r.Context(), s.cfg.DataDir)
	if err != nil {
		s.writeErr(w, mapBackupError(err))
		return
	}
	s.audit(r.Context(), domain.SenderUser, owner.ID, "admin.export", "export", filepath.Base(result.Dir), "ok",
		jsonDetail(map[string]any{"dir": result.Dir, "counts": result.Counts, "redacted": result.Redacted}))
	writeOK(w, result)
}

type importRequest struct {
	ExportPath string `json:"export_path"`
	TargetDir  string `json:"target_dir"`
	Mode       string `json:"mode"`
	Confirm    bool   `json:"confirm"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	var req importRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.ExportPath) == "" {
		s.writeErr(w, errInvalid("export_path 必填"))
		return
	}
	target := req.TargetDir
	if strings.TrimSpace(target) == "" {
		target = s.cfg.DataDir
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		s.writeErr(w, errInvalid("target_dir 不合法"))
		return
	}
	mode := backup.ImportSkip
	if req.Mode == string(backup.ImportError) {
		mode = backup.ImportError
	}
	result, err := backup.Import(r.Context(), absTarget, req.ExportPath, mode, req.Confirm)
	if err != nil {
		s.writeErr(w, mapBackupError(err))
		return
	}
	s.audit(r.Context(), domain.SenderUser, owner.ID, "admin.import", "export", filepath.Base(req.ExportPath), "ok",
		jsonDetail(map[string]any{"target_dir": absTarget, "inserted": result.TotalInserted, "skipped": result.TotalSkipped}))
	writeOK(w, result)
}

type emergencyStopRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleEmergencyStop(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	enabled := true
	var req emergencyStopRequest
	if r.Body != nil {
		_ = decodeJSON(w, r, &req)
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
	}
	value := "false"
	if enabled {
		value = "true"
	}
	if err := s.store.SetSetting(ctx, "emergency_stop", value); err != nil {
		s.writeErr(w, err)
		return
	}
	if _, err := s.store.AppendEvent(ctx, domain.Event{
		Type: "admin.emergency_stop", ObjectType: "system", ObjectID: "emergency_stop",
		ActorType: domain.SenderUser, ActorID: owner.ID,
		Payload: jsonDetail(map[string]any{"emergency_stop": enabled}),
	}); err != nil {
		s.logf("写紧急停止事件失败: %v", err)
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "admin.emergency_stop", "system", "emergency_stop", "ok",
		jsonDetail(map[string]any{"emergency_stop": enabled}))
	writeOK(w, map[string]any{"emergency_stop": enabled})
}

// handleAudit GET /api/audit?object_type=&action=&actor_type=&actor_id=&result=&since=&limit=
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	limit, err := parseInt64Param(q.Get("limit"), 100)
	if err != nil || limit <= 0 || limit > 1000 {
		s.writeErr(w, errInvalid("limit 必须在 1..1000"))
		return
	}
	f := store.AuditFilter{
		ActorType: q.Get("actor_type"), ActorID: q.Get("actor_id"),
		Action: q.Get("action"), ObjectType: q.Get("object_type"), ObjectID: q.Get("object_id"),
		Result: q.Get("result"), Limit: int(limit),
	}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		if seq, err := strconv.ParseInt(raw, 10, 64); err == nil && seq > 0 {
			f.SinceSeq = seq
		} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
			f.SinceCreatedAt = t.UTC().Format(fixedTimeLayout)
		} else {
			s.writeErr(w, errInvalid("since 必须是审计 seq 或 RFC3339 时间"))
			return
		}
	}
	items, err := s.store.QueryAudit(r.Context(), f)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

var _ = context.Background
