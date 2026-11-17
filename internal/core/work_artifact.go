package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ArtifactInput 是创建产出物的输入。
type ArtifactInput struct {
	TaskID       string
	Type         string
	Title        string
	Content      string
	DepartmentID string
}

// CreateArtifact 登记产出物并创建 v1（内容 hash 不可变）。
func (s *WorkService) CreateArtifact(ctx context.Context, actor domain.Agent, in ArtifactInput) (domain.Artifact, domain.ArtifactVersion, error) {
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Content) == "" {
		return domain.Artifact{}, domain.ArtifactVersion{}, domain.NewError(domain.CodeInvalid, "产出物缺少 title/content", 400)
	}
	if in.Type == "" {
		in.Type = "document"
	}
	if in.TaskID != "" {
		if _, err := s.store.GetTask(ctx, in.TaskID); err != nil {
			return domain.Artifact{}, domain.ArtifactVersion{}, domain.NewError(domain.CodeInvalid, "任务不存在："+in.TaskID, 400)
		}
	}
	artifact, err := s.store.CreateArtifact(ctx, domain.Artifact{
		ID: domain.NewID("art"), Type: in.Type, Title: in.Title, TaskID: in.TaskID,
		DepartmentID: in.DepartmentID, Status: "draft", CreatedBy: actor.ID, CreatedAt: s.nowString(),
	})
	if err != nil {
		return domain.Artifact{}, domain.ArtifactVersion{}, fmt.Errorf("core: 创建产出物失败: %w", err)
	}
	version, err := s.addVersion(ctx, actor, artifact, in.Content, "")
	if err != nil {
		return domain.Artifact{}, domain.ArtifactVersion{}, err
	}
	s.emit(ctx, "artifact.created", "artifact", artifact.ID, "agent", actor.ID, map[string]any{"task_id": artifact.TaskID, "version": version.Version})
	s.audit(ctx, "agent", actor.ID, "artifact.create", "artifact", artifact.ID, "ok", map[string]any{"task_id": artifact.TaskID, "hash": version.ContentHash})
	return artifact, version, nil
}

// AddArtifactVersion 新增不可变版本（版本号递增，内容 hash 唯一）。
func (s *WorkService) AddArtifactVersion(ctx context.Context, actor domain.Agent, artifactID, content, supersedes string) (domain.ArtifactVersion, error) {
	artifact, err := s.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	if strings.TrimSpace(content) == "" {
		return domain.ArtifactVersion{}, domain.NewError(domain.CodeInvalid, "版本内容不能为空", 400)
	}
	versions, err := s.store.ListArtifactVersions(ctx, artifactID)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	if len(versions) > 0 && artifact.CreatedBy != actor.ID && !(actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID == artifact.DepartmentID) && actor.Kind != domain.AgentKindAssistant {
		return domain.ArtifactVersion{}, NewForbidden("只有作者/部门主管/助理可以提交新版本", "请由产出物作者提交新版本。")
	}
	maxVersion := 0
	for _, v := range versions {
		if v.Version > maxVersion {
			maxVersion = v.Version
			if supersedes == "" {
				supersedes = v.ID
			}
		}
	}
	_ = maxVersion
	version, err := s.addVersion(ctx, actor, artifact, content, supersedes)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	s.emit(ctx, "artifact.updated", "artifact", artifact.ID, "agent", actor.ID, map[string]any{"version": version.Version, "hash": version.ContentHash})
	s.audit(ctx, "agent", actor.ID, "artifact.version", "artifact", artifact.ID, "ok", map[string]any{"version": version.Version, "hash": version.ContentHash})
	return version, nil
}

func (s *WorkService) addVersion(ctx context.Context, actor domain.Agent, artifact domain.Artifact, content, supersedes string) (domain.ArtifactVersion, error) {
	sum := sha256.Sum256([]byte(content))
	versions, _ := s.store.ListArtifactVersions(ctx, artifact.ID)
	next := 1
	for _, v := range versions {
		if v.Version >= next {
			next = v.Version + 1
		}
	}
	return s.store.CreateArtifactVersion(ctx, domain.ArtifactVersion{
		ID: domain.NewID("artv"), ArtifactID: artifact.ID, Version: next,
		Content: content, ContentHash: hex.EncodeToString(sum[:]),
		AuthorID: actor.ID, Supersedes: supersedes, CreatedAt: s.nowString(),
	})
}

// ArtifactRefsByTask 返回任务关联的产出物版本引用（G3 卡/任务详情用）。
func (s *WorkService) ArtifactRefsByTask(ctx context.Context, taskID string) ([]ArtifactRef, error) {
	artifacts, err := s.store.ListArtifactsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var refs []ArtifactRef
	for _, a := range artifacts {
		versions, err := s.store.ListArtifactVersions(ctx, a.ID)
		if err != nil || len(versions) == 0 {
			continue
		}
		latest := versions[0]
		for _, v := range versions {
			if v.Version > latest.Version {
				latest = v
			}
		}
		refs = append(refs, ArtifactRef{ArtifactID: a.ID, Version: latest.Version, ContentHash: latest.ContentHash, Title: a.Title})
	}
	return refs, nil
}
