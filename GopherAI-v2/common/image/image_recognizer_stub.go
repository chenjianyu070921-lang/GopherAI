//go:build !cgo

package image

import "fmt"

// 未启用 CGO 时的桩实现：保证聊天/RAG 等其他功能可以正常编译和启动。
// 如需图像识别：安装 mingw-w64(gcc) 与 onnxruntime.dll 后，以 CGO_ENABLED=1 启动，
// 编译器会自动改用 image_recognizer.go 的真实实现。

type ImageRecognizer struct{}

func NewImageRecognizer(modelPath, labelPath string, inputH, inputW int) (*ImageRecognizer, error) {
	return nil, fmt.Errorf("图像识别未启用：需要 CGO_ENABLED=1、gcc(mingw-w64) 及 onnxruntime.dll")
}

func (r *ImageRecognizer) Close() {}

func (r *ImageRecognizer) PredictFromBuffer(buf []byte) (string, error) {
	return "", fmt.Errorf("图像识别未启用")
}
