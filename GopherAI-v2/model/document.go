package model

import (
	"time"

	"gorm.io/gorm"
)

// 文档状态
const (
	DocStatusPending = "pending" // 已登记，尚未完成向量化（崩溃恢复时会被清理）
	DocStatusReady   = "ready"   // 向量化完成，可被检索
)

// Document 用户上传到知识库的文档
type Document struct {
	ID           string         `gorm:"primaryKey;size:36" json:"id"`
	UserName     string         `gorm:"size:64;index:idx_documents_user_name" json:"user_name"`
	OriginalName string         `gorm:"size:255" json:"original_name"`
	StoragePath  string         `gorm:"size:512" json:"storage_path"`
	Size         int64          `json:"size"`
	ChunkCount   int            `json:"chunk_count"`
	Status       string         `gorm:"size:16;index;default:pending" json:"status"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"-"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}
