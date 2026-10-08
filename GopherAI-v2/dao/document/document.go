package document

import (
	"GopherAI/common/mysql"
	"GopherAI/model"
)

// Create 登记文档记录
func Create(doc *model.Document) error {
	return mysql.DB.Create(doc).Error
}

// MarkReady 将文档标记为可用并回写切块数量
func MarkReady(id string, chunkCount int) error {
	return mysql.DB.Model(&model.Document{}).Where("id = ?", id).
		Updates(map[string]any{"status": model.DocStatusReady, "chunk_count": chunkCount}).Error
}

// ListReadyByUsername 查询用户所有可用文档（按上传时间倒序）
func ListReadyByUsername(username string) ([]*model.Document, error) {
	var docs []*model.Document
	err := mysql.DB.Where("user_name = ? AND status = ?", username, model.DocStatusReady).
		Order("created_at desc").Find(&docs).Error
	return docs, err
}

// GetReadyByIDAndUsername 按归属查询单个可用文档
func GetReadyByIDAndUsername(id, username string) (*model.Document, error) {
	doc := new(model.Document)
	err := mysql.DB.Where("id = ? AND user_name = ? AND status = ?", id, username, model.DocStatusReady).
		First(doc).Error
	return doc, err
}

// CountReadyByUsername 统计用户的可用文档数
func CountReadyByUsername(username string) (int64, error) {
	var count int64
	err := mysql.DB.Model(&model.Document{}).
		Where("user_name = ? AND status = ?", username, model.DocStatusReady).Count(&count).Error
	return count, err
}

// SumSizeReadyByUsername 统计用户所有可用文档的总字节数
func SumSizeReadyByUsername(username string) (int64, error) {
	var total int64
	err := mysql.DB.Model(&model.Document{}).
		Where("user_name = ? AND status = ?", username, model.DocStatusReady).
		Select("COALESCE(SUM(size), 0)").Scan(&total).Error
	return total, err
}

// GetByID 按主键查询文档（不限状态与归属，软删除记录不可见），供启动对账使用
func GetByID(id string) (*model.Document, error) {
	doc := new(model.Document)
	err := mysql.DB.Where("id = ?", id).First(doc).Error
	return doc, err
}

// SoftDelete 软删除文档
func SoftDelete(id string) error {
	return mysql.DB.Where("id = ?", id).Delete(&model.Document{}).Error
}

// ListNonReady 查询所有未完成向量化的文档（供启动时崩溃恢复）
func ListNonReady() ([]*model.Document, error) {
	var docs []*model.Document
	err := mysql.DB.Where("status <> ?", model.DocStatusReady).Find(&docs).Error
	return docs, err
}

// HardDelete 物理删除文档记录
func HardDelete(id string) error {
	return mysql.DB.Unscoped().Where("id = ?", id).Delete(&model.Document{}).Error
}
