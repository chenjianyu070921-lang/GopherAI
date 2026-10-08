package file

import (
	"GopherAI/common/code"
	"GopherAI/common/rag"
	"GopherAI/controller"
	"GopherAI/model"
	"GopherAI/service/file"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DocInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ChunkCount int    `json:"chunk_count"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type (
	UploadFileResponse struct {
		DocInfo
		controller.Response
	}

	ListFilesResponse struct {
		Docs []DocInfo `json:"docs"`
		controller.Response
	}
)

func toDocInfo(d *model.Document) DocInfo {
	return DocInfo{
		ID:         d.ID,
		Name:       d.OriginalName,
		Size:       d.Size,
		ChunkCount: d.ChunkCount,
		CreatedAt:  d.CreatedAt.Format("2006-01-02 15:04:05"),
	}
}

// mapServiceError 将 service 业务错误映射为状态码
func mapServiceError(err error) code.Code {
	switch {
	case errors.Is(err, file.ErrInvalidFileType):
		return code.CodeInvalidParams
	case errors.Is(err, file.ErrFileTooLarge):
		return code.CodeFileTooLarge
	case errors.Is(err, file.ErrInvalidText):
		return code.CodeFileInvalidText
	case errors.Is(err, file.ErrEmptyContent):
		return code.CodeInvalidParams
	case errors.Is(err, rag.ErrAPIKeyNotConfigured):
		return code.CodeRAGNotConfigured
	case errors.Is(err, file.ErrDocumentNotFound):
		return code.CodeRecordNotFound
	case errors.Is(err, file.ErrInvalidUserName):
		return code.CodeInvalidParams
	case errors.Is(err, file.ErrQuotaExceeded):
		return code.CodeQuotaExceeded
	default:
		return code.CodeServerBusy
	}
}

func UploadRagFile(c *gin.Context) {
	res := new(UploadFileResponse)
	uploadedFile, err := c.FormFile("file")
	if err != nil {
		log.Println("FormFile fail ", err)
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidParams))
		return
	}

	username := c.GetString("userName")
	if username == "" {
		log.Println("Username not found in context")
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidToken))
		return
	}

	doc, err := file.Upload(username, uploadedFile)
	if err != nil {
		log.Println("UploadRagFile fail ", err)
		c.JSON(http.StatusOK, res.CodeOf(mapServiceError(err)))
		return
	}

	res.Success()
	res.DocInfo = toDocInfo(doc)
	c.JSON(http.StatusOK, res)
}

func ListRagFiles(c *gin.Context) {
	res := new(ListFilesResponse)
	username := c.GetString("userName")
	if username == "" {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidToken))
		return
	}

	docs, err := file.List(username)
	if err != nil {
		log.Println("ListRagFiles fail ", err)
		c.JSON(http.StatusOK, res.CodeOf(code.CodeServerBusy))
		return
	}

	res.Docs = make([]DocInfo, 0, len(docs))
	for _, d := range docs {
		res.Docs = append(res.Docs, toDocInfo(d))
	}
	res.Success()
	c.JSON(http.StatusOK, res)
}

func DeleteRagFile(c *gin.Context) {
	res := new(controller.Response)
	id := c.Param("id")

	// 只接受合法 uuid，杜绝参数注入
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidParams))
		return
	}

	username := c.GetString("userName")
	if username == "" {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidToken))
		return
	}

	if err := file.Delete(username, id); err != nil {
		log.Println("DeleteRagFile fail ", err)
		c.JSON(http.StatusOK, res.CodeOf(mapServiceError(err)))
		return
	}

	res.Success()
	c.JSON(http.StatusOK, res)
}
