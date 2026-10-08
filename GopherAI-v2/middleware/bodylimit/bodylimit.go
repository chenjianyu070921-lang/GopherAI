package bodylimit

import (
	"GopherAI/config"
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxUploadBody 限制上传请求体的总字节数。
// 必须在 c.FormFile 触发 multipart 解析之前生效：service 层的 fh.Size 校验
// 发生在整个请求体接收完毕之后，挡不住超大 body 对磁盘/带宽的消耗。
// 上限取「单文件大小 + 1MB」余量（multipart 包装与表单字段开销）。
func MaxUploadBody() gin.HandlerFunc {
	maxMB := config.GetConfig().UploadConfig.MaxSizeMB
	if maxMB <= 0 {
		maxMB = 10
	}
	limit := int64(maxMB+1) * 1024 * 1024

	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}
