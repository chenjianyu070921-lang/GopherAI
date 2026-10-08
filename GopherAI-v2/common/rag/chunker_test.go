package rag

import (
	"strings"
	"testing"
)

func TestSplitText_Empty(t *testing.T) {
	if got := SplitText("   \n  ", 800, 100); got != nil {
		t.Fatalf("expected nil for blank text, got %v", got)
	}
}

func TestSplitText_SingleShortParagraph(t *testing.T) {
	text := "这是一个短段落。"
	got := SplitText(text, 800, 100)
	if len(got) != 1 || got[0] != text {
		t.Fatalf("expected single unchanged chunk, got %#v", got)
	}
}

func TestSplitText_MultipleShortParagraphsMerged(t *testing.T) {
	paragraphs := []string{"# 标题", "第一段内容", "第二段内容", "第三段内容"}
	got := SplitText(strings.Join(paragraphs, "\n"), 800, 100)
	if len(got) != 1 {
		t.Fatalf("expected short paragraphs merged into 1 chunk, got %d: %#v", len(got), got)
	}
	// Markdown 标题必须保留，且不与正文分离
	if !strings.HasPrefix(got[0], "# 标题") {
		t.Fatalf("heading should be kept at chunk start, got %q", got[0])
	}
	for _, p := range paragraphs[1:] {
		if !strings.Contains(got[0], p) {
			t.Fatalf("chunk missing paragraph %q: %q", p, got[0])
		}
	}
}

func TestSplitText_LongParagraphNoPunctuationHardCut(t *testing.T) {
	text := strings.Repeat("字", 1800) // 无任何标点的超长段落
	got := SplitText(text, 500, 50)
	if len(got) < 3 {
		t.Fatalf("expected hard-cut into at least 3 chunks, got %d", len(got))
	}
	for i, c := range got {
		n := len([]rune(c))
		// 除重叠外，块长度不应大幅超过 chunkSize（首块无重叠，其余块允许 overlap 重叠）
		if i == 0 && n != 500 {
			t.Fatalf("first chunk length = %d, want 500", n)
		}
		if n > 560 { // 允许 overlap 50 + 连接换行
			t.Fatalf("chunk %d too long: %d runes", i, n)
		}
	}
}

func TestSplitText_SentenceBoundary(t *testing.T) {
	// 10 个各 60 rune 的句子，共 600 rune；chunkSize=200 → 应在句子边界切成约 3 块
	var sb strings.Builder
	sentence := strings.Repeat("啊", 59) + "。"
	for i := 0; i < 10; i++ {
		sb.WriteString(sentence)
	}
	got := SplitText(sb.String(), 200, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks at sentence boundaries, got %d", len(got))
	}
	for i, c := range got {
		if !strings.HasSuffix(c, "。") {
			t.Fatalf("chunk %d should end with sentence delimiter: %q", i, c)
		}
	}
}

func TestSplitText_OverlapContent(t *testing.T) {
	// 构造 4 个各 300 rune 的段落，chunkSize=500 overlap=100
	makeParagraph := func(i int) string {
		// 每段用不同标记字符开头，便于验证重叠来源
		return strings.Repeat(string(rune('A'+i)), 300)
	}
	var sb strings.Builder
	for i := 0; i < 4; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(makeParagraph(i))
	}
	got := SplitText(sb.String(), 500, 100)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(got))
	}
	// 第二个块的开头应当包含第一个块尾部的重叠内容（A 字符）
	if !strings.HasPrefix(got[1], strings.Repeat("A", 100)) {
		t.Fatalf("chunk 2 should start with overlap from chunk 1, got prefix %q", firstRunes(got[1], 100))
	}
}

func TestSplitText_TinyLastChunkMerged(t *testing.T) {
	big := strings.Repeat("长", 780)
	tiny := "短"
	got := SplitText(big+"\n"+tiny, 800, 100)
	// 小块应并入前块，而不是单独成块
	if len(got) != 1 {
		t.Fatalf("expected tiny last chunk merged, got %d chunks", len(got))
	}
	if !strings.Contains(got[0], tiny) {
		t.Fatalf("merged chunk should contain the tiny part: %q", got[0])
	}
}

func TestSplitText_FencedCodeKeptAtomic(t *testing.T) {
	code := "```go\n" + strings.Repeat("x", 40) + "\n```"
	text := strings.Repeat("段", 90) + "\n" + code + "\n" + strings.Repeat("尾", 90)
	got := SplitText(text, 100, 0)
	// 期望切成 [90字段落, 完整代码块, 90字段落]，代码块不被拆散
	var codeChunk string
	for _, c := range got {
		n := strings.Count(c, "```")
		if n != 0 && n != 2 {
			t.Fatalf("chunk has unbalanced code fences (%d): %q", n, c)
		}
		if n == 2 {
			if codeChunk != "" {
				t.Fatalf("more than one chunk contains code: %#v", got)
			}
			codeChunk = c
		}
	}
	if codeChunk != code {
		t.Fatalf("code block altered:\n got=%q\nwant=%q", codeChunk, code)
	}
}

func TestSplitText_OversizeFencedCodeSplitWithFences(t *testing.T) {
	inner := strings.Repeat("y", 336) // 92*3+60：末片 60 rune，不触发过小末块合并
	code := "```go\n" + inner + "\n```"
	got := SplitText(code, 100, 0)
	// 300 字符代码体 / 每片预算 92 → 4 片；每片都必须带成对围栏与语言标识
	if len(got) != 4 {
		t.Fatalf("expected 4 fenced pieces, got %d: %#v", len(got), got)
	}
	var reassembled strings.Builder
	for i, c := range got {
		if !strings.HasPrefix(c, "```go") {
			t.Fatalf("piece %d missing opening fence with lang: %q", i, c)
		}
		if !strings.HasSuffix(c, "```") {
			t.Fatalf("piece %d missing closing fence: %q", i, c)
		}
		if strings.Count(c, "```") != 2 {
			t.Fatalf("piece %d must have exactly 2 fences: %q", i, c)
		}
		// 去掉首行围栏与末尾围栏，取代码体
		body := strings.TrimSuffix(strings.TrimPrefix(c, "```go\n"), "\n```")
		reassembled.WriteString(body)
	}
	if reassembled.String() != inner {
		t.Fatalf("reassembled code body does not match original, len=%d want %d",
			reassembled.Len(), len(inner))
	}
}

func TestSplitText_TableKeptAtomic(t *testing.T) {
	table := "| 姓名 | 年龄 |\n| --- | --- |\n| 张三 | 20 |\n| 李四 | 21 |"
	text := strings.Repeat("段", 90) + "\n" + table + "\n" + strings.Repeat("尾", 90)
	got := SplitText(text, 100, 0)
	found := false
	for _, c := range got {
		if strings.Contains(c, "| --- |") {
			if found {
				t.Fatalf("table duplicated across chunks: %#v", got)
			}
			found = true
			if c != table {
				t.Fatalf("table altered:\n got=%q\nwant=%q", c, table)
			}
		}
	}
	if !found {
		t.Fatalf("table separator not found in any chunk: %#v", got)
	}
}

func TestSplitText_OversizeTableRepeatsHeader(t *testing.T) {
	header := "| 名称 | 数值 |"
	sep := "| --- | --- |"
	var sb strings.Builder
	sb.WriteString(header + "\n" + sep)
	for i := 0; i < 20; i++ {
		sb.WriteString("\n| 项目 | " + strings.Repeat("数", 6) + " |")
	}
	got := SplitText(sb.String(), 100, 0)
	// 每片可放 4 行数据（19 + 4*17 = 87），20 行 → 5 片
	if len(got) != 5 {
		t.Fatalf("expected 5 table pieces, got %d: %#v", len(got), got)
	}
	dataRows := 0
	for i, c := range got {
		if !strings.HasPrefix(c, header) {
			t.Fatalf("piece %d must start with header: %q", i, c)
		}
		if strings.Count(c, sep) != 1 {
			t.Fatalf("piece %d must contain exactly one separator: %q", i, c)
		}
		// 统计数据行（去掉表头与分隔行）
		dataRows += len(strings.Split(c, "\n")) - 2
	}
	if dataRows != 20 {
		t.Fatalf("data row count = %d, want 20", dataRows)
	}
}

func TestSplitText_HeadingBreadcrumbOnLaterChunks(t *testing.T) {
	text := "# 标题\n" + strings.Repeat("字", 250)
	got := SplitText(text, 100, 0)
	// 首块是标题本身，不补面包屑；后续块都应携带章节面包屑
	if got[0] != "# 标题" {
		t.Fatalf("first chunk should be the heading, got %q", got[0])
	}
	for i := 1; i < len(got); i++ {
		if !strings.HasPrefix(got[i], "[章节：标题]") {
			t.Fatalf("chunk %d missing heading breadcrumb: %q", i, got[i])
		}
	}
}

func TestSplitText_BOMAndWindowsNewlines(t *testing.T) {
	text := "\uFEFF第一段。\r\n第二段。"
	got := SplitText(text, 800, 100)
	if len(got) != 1 {
		t.Fatalf("expected single chunk, got %d", len(got))
	}
	if strings.HasPrefix(got[0], "\uFEFF") {
		t.Fatalf("BOM should be stripped: %q", got[0])
	}
	if strings.Contains(got[0], "\r") {
		t.Fatalf("CR should be normalized: %q", got[0])
	}
}
