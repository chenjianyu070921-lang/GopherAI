package file

import (
	"GopherAI/common/rag"
	redisPkg "GopherAI/common/redis"
	"GopherAI/config"
	documentdao "GopherAI/dao/document"
	"GopherAI/model"
	"GopherAI/utils"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// 业务错误（controller 层据此映射状态码）
var (
	ErrInvalidFileType  = errors.New("只允许上传 .md 或 .txt 文件")
	ErrFileTooLarge     = errors.New("文件超过大小上限")
	ErrInvalidText      = errors.New("文件内容不是有效的文本")
	ErrEmptyContent     = errors.New("文件内容为空")
	ErrDocumentNotFound = errors.New("文档不存在")
	ErrInvalidUserName  = errors.New("非法用户名")
	ErrQuotaExceeded    = errors.New("超出知识库配额")
)

const defaultMaxSizeMB = 10

func uploadDir() string {
	dir := config.GetConfig().UploadConfig.UploadDir
	if dir == "" {
		return "./uploads"
	}
	return dir
}

func maxFileSize() int64 {
	mb := config.GetConfig().UploadConfig.MaxSizeMB
	if mb <= 0 {
		mb = defaultMaxSizeMB
	}
	return int64(mb) * 1024 * 1024
}

// resolveUserDir 解析并校验用户文件目录，防御路径穿越：
// 用户名虽然由系统生成（11 位数字），JWT 中仍可能携带任意字符串，
// 因此在文件系统边界再做一次校验 + 目录包含关系确认（纵深防御）。
func resolveUserDir(username string) (string, error) {
	if username == "" || username == "." || username == ".." ||
		strings.ContainsAny(username, `/\`) || strings.Contains(username, "..") ||
		strings.ContainsRune(username, 0) {
		return "", ErrInvalidUserName
	}

	base, err := filepath.Abs(uploadDir())
	if err != nil {
		return "", err
	}
	dir := filepath.Clean(filepath.Join(base, username))

	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidUserName
	}
	return dir, nil
}

// Upload 上传知识库文档：
// 校验 → 配额检查 → 登记 pending 记录 → 落盘 → 确保索引 → 切块向量化 → 标记 ready。
func Upload(username string, fh *multipart.FileHeader) (*model.Document, error) {
	// 0. 解析用户目录（同时校验用户名）
	userDir, err := resolveUserDir(username)
	if err != nil {
		return nil, err
	}

	// 1. 校验扩展名（大小写不敏感）
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if ext != ".md" && ext != ".txt" {
		return nil, ErrInvalidFileType
	}

	// 2. 校验大小
	if fh.Size > maxFileSize() {
		return nil, ErrFileTooLarge
	}

	// 3. 读取内容，校验 UTF-8 与非空
	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	content, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(content) {
		return nil, ErrInvalidText
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, ErrEmptyContent
	}

	cfg := config.GetConfig()

	// 4. 先在内存切块：既用于配额检查（片段数上限），也避免落盘后才发现超限
	chunks := rag.SplitText(string(content), cfg.UploadConfig.ChunkSize, cfg.UploadConfig.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyContent
	}
	if max := cfg.UploadConfig.MaxChunks; max > 0 && len(chunks) > max {
		return nil, fmt.Errorf("%w：单文档片段数 %d 超过上限 %d", ErrQuotaExceeded, len(chunks), max)
	}

	// 5. 配额检查：文档数与总容量
	if err := checkQuota(username, int64(len(content))); err != nil {
		return nil, err
	}

	// 6. 先登记 pending 记录（审计轨迹，崩溃后可恢复）
	docID := utils.GenerateUUID()
	storagePath := filepath.Join(userDir, docID+ext)

	doc := &model.Document{
		ID:           docID,
		UserName:     username,
		OriginalName: fh.Filename,
		StoragePath:  storagePath,
		Size:         int64(len(content)),
		ChunkCount:   0,
		Status:       model.DocStatusPending,
	}
	if err := documentdao.Create(doc); err != nil {
		return nil, err
	}

	// 7. 落盘
	if err := os.MkdirAll(userDir, 0755); err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}
	if err := os.WriteFile(storagePath, content, 0644); err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}

	// 8. 确保用户索引存在（幂等）
	if err := rag.EnsureUserIndex(context.Background(), username); err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}

	// 9. 切块 + 向量化（切块结果与第 4 步一致）
	indexer, err := rag.NewRAGIndexer(context.Background(), username)
	if err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}
	chunkCount, err := indexer.IndexDocument(context.Background(), docID, fh.Filename, storagePath)
	if err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}

	// 10. 标记可用
	if err := documentdao.MarkReady(docID, chunkCount); err != nil {
		cleanupFailedUpload(username, docID, storagePath)
		return nil, err
	}

	doc.Status = model.DocStatusReady
	doc.ChunkCount = chunkCount
	log.Printf("Document uploaded and indexed: user=%s doc=%s chunks=%d", username, docID, chunkCount)
	return doc, nil
}

// checkQuota 检查用户知识库的文档数与总容量配额
func checkQuota(username string, incomingSize int64) error {
	cfg := config.GetConfig().UploadConfig

	if max := cfg.MaxDocs; max > 0 {
		count, err := documentdao.CountReadyByUsername(username)
		if err != nil {
			return err
		}
		if int(count) >= max {
			return fmt.Errorf("%w：文档数量已达上限 %d", ErrQuotaExceeded, max)
		}
	}

	if maxMB := cfg.MaxTotalMB; maxMB > 0 {
		total, err := documentdao.SumSizeReadyByUsername(username)
		if err != nil {
			return err
		}
		limit := int64(maxMB) * 1024 * 1024
		if total+incomingSize > limit {
			return fmt.Errorf("%w：总容量已达上限 %dMB", ErrQuotaExceeded, maxMB)
		}
	}
	return nil
}

// cleanupFailedUpload 上传失败后的级联清理。
// 关键语义：向量块删除失败（如 Redis 不可用）时立即中止，保留磁盘文件与
// pending 记录作为「锚点」，下次启动 RecoverPending 会重试，避免孤儿向量。
func cleanupFailedUpload(username, docID, storagePath string) {
	if err := rag.RemoveDocument(context.Background(), username, docID); err != nil {
		log.Printf("cleanup: remove chunks failed for doc=%s, keep pending record for retry: %v", docID, err)
		return
	}
	if err := os.Remove(storagePath); err != nil && !os.IsNotExist(err) {
		log.Printf("cleanup: remove file failed for doc=%s: %v", docID, err)
	}
	if err := documentdao.HardDelete(docID); err != nil {
		log.Printf("cleanup: remove db record failed for doc=%s: %v", docID, err)
	}
}

// List 查询用户的知识库文档
func List(username string) ([]*model.Document, error) {
	return documentdao.ListReadyByUsername(username)
}

// Delete 删除单个文档（含归属校验）
func Delete(username, id string) error {
	doc, err := documentdao.GetReadyByIDAndUsername(id, username)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrDocumentNotFound
	}
	if err != nil {
		return err
	}

	// 先删文件
	if err := os.Remove(doc.StoragePath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// 再删向量 chunk（失败则保留记录，避免孤儿 chunk）
	if err := rag.RemoveDocument(context.Background(), username, doc.ID); err != nil {
		return err
	}

	// 最后软删 DB 记录
	if err := documentdao.SoftDelete(doc.ID); err != nil {
		return err
	}

	// 注意：文档数为 0 时也保留用户索引，不做 FT.DROPINDEX。
	// 空 FLAT 索引开销极小，下次上传直接复用；同时可避免“删除(索引消失)与
	// 并发上传(EnsureUserIndex 已通过)交叉”导致新 chunk 没有索引的竞态。
	return nil
}

// StartupMaintenance 启动维护：等待 Redis 就绪 → 恢复未完成上传 → 孤儿数据对账。
func StartupMaintenance() {
	if !waitForRedis(10) {
		log.Printf("startup maintenance: redis not ready, skip recovery/orphan reconciliation")
		return
	}
	RecoverPending()
	ReconcileOrphans()
}

// waitForRedis PING 轮询等待 Redis 就绪（redis.Init 只建客户端不探活）
func waitForRedis(maxWaitSeconds int) bool {
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(maxWaitSeconds)*time.Second)
	defer cancel()
	for {
		if err := redisPkg.Rdb.Ping(ctx).Err(); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		default:
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// RecoverPending 清理上次进程崩溃残留的未完成上传。
// 语义与 cleanupFailedUpload 一致：向量块删除失败则保留文件与 pending 锚点，
// 等待下次启动重试，而不是强行删文件/删记录制造孤儿向量。
func RecoverPending() {
	docs, err := documentdao.ListNonReady()
	if err != nil {
		log.Printf("recover: list non-ready docs failed: %v", err)
		return
	}
	recovered := 0
	for _, doc := range docs {
		if err := rag.RemoveDocument(context.Background(), doc.UserName, doc.ID); err != nil {
			log.Printf("recover: remove chunks failed for doc=%s, keep pending anchor: %v", doc.ID, err)
			continue
		}
		if err := os.Remove(doc.StoragePath); err != nil && !os.IsNotExist(err) {
			log.Printf("recover: remove file failed for doc=%s: %v", doc.ID, err)
		}
		if err := documentdao.HardDelete(doc.ID); err != nil {
			log.Printf("recover: hard delete failed for doc=%s: %v", doc.ID, err)
		}
		recovered++
	}
	if recovered > 0 {
		log.Printf("recover: cleaned up %d unfinished upload(s)", recovered)
	}
}

// ReconcileOrphans 对账两个崩溃窗口产生的孤儿数据：
//  1. Redis 里存在向量、但 DB 中已无对应文档记录（向量化成功后、MarkReady/落库前崩溃，
//     或旧版本残留）→ 删除该文档的全部向量；
//  2. 磁盘上存在文件、但 DB 中已无记录 → 删除文件并清理空目录。
func ReconcileOrphans() {
	// 1. Redis 孤儿向量
	refs, err := rag.ScanAllDocRefs(context.Background())
	if err != nil {
		log.Printf("reconcile: scan doc refs failed: %v", err)
	} else {
		removed := 0
		for _, ref := range refs {
			_, err := documentdao.GetByID(ref.DocID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := rag.RemoveDocument(context.Background(), ref.User, ref.DocID); err != nil {
					log.Printf("reconcile: remove orphan chunks failed user=%s doc=%s: %v", ref.User, ref.DocID, err)
					continue
				}
				removed++
			} else if err != nil {
				log.Printf("reconcile: lookup doc %s failed: %v", ref.DocID, err)
			}
		}
		if removed > 0 {
			log.Printf("reconcile: removed %d orphan vector document(s)", removed)
		}
	}

	// 2. 磁盘孤儿文件
	base, err := filepath.Abs(uploadDir())
	if err != nil {
		log.Printf("reconcile: resolve upload dir failed: %v", err)
		return
	}
	removedFiles := 0
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("reconcile: read upload dir failed: %v", err)
		return
	}
	for _, userEntry := range entries {
		if !userEntry.IsDir() {
			continue
		}
		userDir := filepath.Join(base, userEntry.Name())
		files, err := os.ReadDir(userDir)
		if err != nil {
			log.Printf("reconcile: read user dir %s failed: %v", userDir, err)
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			docID := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			_, err := documentdao.GetByID(docID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := os.Remove(filepath.Join(userDir, f.Name())); err != nil {
					log.Printf("reconcile: remove orphan file %s failed: %v", f.Name(), err)
					continue
				}
				removedFiles++
			} else if err != nil {
				log.Printf("reconcile: lookup doc %s failed: %v", docID, err)
			}
		}
		// 清理空的用户目录（失败无所谓）
		if remaining, err := os.ReadDir(userDir); err == nil && len(remaining) == 0 {
			_ = os.Remove(userDir)
		}
	}
	if removedFiles > 0 {
		log.Printf("reconcile: removed %d orphan file(s)", removedFiles)
	}
}
