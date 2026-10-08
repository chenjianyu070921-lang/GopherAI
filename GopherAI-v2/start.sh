#!/usr/bin/env bash
# GopherAI-v2 本地启动脚本
# 用法（Git Bash）: ./start.sh
#
# 真实密钥不要再写进本脚本。请复制 local_env.sh.example 为 local_env.sh 并填入自己的密钥；
# local_env.sh 已被 .gitignore 忽略，不会提交。

# 加载本地密钥环境（若存在）
if [ -f ./local_env.sh ]; then
  # shellcheck disable=SC1091
  source ./local_env.sh
fi

# 普通聊天（火山方舟 Coding Plan）
export OPENAI_BASE_URL="${OPENAI_BASE_URL:-https://ark.cn-beijing.volces.com/api/coding/v3}"
export OPENAI_MODEL_NAME="${OPENAI_MODEL_NAME:-doubao-seed-code}"

echo "GopherAI-v2 启动中 (chat model: $OPENAI_MODEL_NAME)"

if [ -z "$OPENAI_API_KEY" ]; then
  echo "警告：未设置 OPENAI_API_KEY（普通聊天不可用）。请在 local_env.sh 或当前 shell 中配置。"
fi
if [ -z "$DASHSCOPE_API_KEY" ]; then
  echo "警告：未设置 DASHSCOPE_API_KEY（RAG 知识库不可用，普通聊天不受影响）。"
fi
if [ -z "$JWT_KEY" ]; then
  echo "提示：未设置 JWT_KEY，将使用 config.toml 中的旧密钥（建议设置高熵随机值并轮换旧密钥）。"
fi

go run main.go
