package router

import (
	"GopherAI/controller/file"
	"GopherAI/middleware/bodylimit"

	"github.com/gin-gonic/gin"
)

func FileRouter(r *gin.RouterGroup) {
	// MaxBytesReader 必须在 multipart 解析前生效，在入口处截断超大请求体
	r.POST("/upload", bodylimit.MaxUploadBody(), file.UploadRagFile)
	r.GET("/list", file.ListRagFiles)
	r.DELETE("/:id", file.DeleteRagFile)
}
