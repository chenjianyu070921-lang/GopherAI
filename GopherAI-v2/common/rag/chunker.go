package rag

import (
	"strings"
)

// 默认切块参数（配置缺失时兜底）
const (
	defaultChunkSize  = 800
	defaultOverlap    = 100
	sentenceDelimiter = "。！？!?；;"
)

// unitKind 语义单元类型
type unitKind int

const (
	kindText    unitKind = iota // 普通段落 / 句子 / 硬切片
	kindHeading                 // Markdown 标题
	kindCode                    // 围栏代码块（整块不可分割）
	kindTable                   // Markdown 表格（整块不可分割）
)

// unit 打包前的最小语义单元
type unit struct {
	text  string
	kind  unitKind
	level int // heading 的级别（1~6）
}

// SplitText 将长文本切分为带重叠的文本块。
// 设计要点：
//   - Markdown 感知：围栏代码块、完整表格作为不可分割单元（本身超尺寸才做带围栏/表头复制的硬切）；
//     标题作为章节信号，跨块时给后续块补「章节面包屑」，避免语义漂移；
//   - 按 rune 计数，中文友好；段落优先聚合，超长段落按句子边界、再按 rune 硬切；
//   - 相邻块从上个块尾部取 overlap 长度内容作为重叠；
//   - 最后一个块过小时并入前一个块。
func SplitText(text string, chunkSize, overlap int) []string {
	// 统一换行符、去掉 BOM
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if overlap < 0 {
		overlap = defaultOverlap
	}
	// overlap 不能超过块大小的一半，否则重叠内容会把新块撑爆
	if overlap > chunkSize/2 {
		overlap = chunkSize / 2
	}

	// 1. 解析语义单元；超长单元就地展开
	units := expandUnits(parseBlocks(text), chunkSize)
	if len(units) == 0 {
		return nil
	}

	// 2. 贪心打包，维护章节面包屑
	chunks := make([]string, 0, len(units))
	current := ""
	currentLen := 0
	breadcrumb := ""           // 当前章节面包屑（如 "章 > 节"）
	startsWithHeading := false // 当前块是否以标题行开头（是则不用补面包屑）

	flush := func() {
		if strings.TrimSpace(current) == "" {
			current = ""
			currentLen = 0
			return
		}
		out := current
		// 块不是以标题开头、但章节上下文非空时，补一行章节面包屑
		if !startsWithHeading && breadcrumb != "" {
			out = "[章节：" + breadcrumb + "]\n" + out
		}
		chunks = append(chunks, strings.TrimSpace(out))
		current = ""
		currentLen = 0
		startsWithHeading = false
	}

	for _, u := range units {
		if u.kind == kindHeading {
			breadcrumb = updateBreadcrumb(breadcrumb, u.level, strings.TrimSpace(u.text))
		}

		unitLen := len([]rune(u.text))
		addedLen := unitLen
		if current != "" {
			addedLen++
		}

		if current != "" && currentLen+addedLen > chunkSize {
			prev := current
			prevBreadcrumb := breadcrumb
			flush()

			// 用上个块尾部的 overlap 长度内容作为新块前缀（面包屑沿用同章节）
			if overlap > 0 {
				tail := lastRunes(prev, overlap)
				if tail != "" {
					current = tail
					currentLen = len([]rune(tail))
					breadcrumb = prevBreadcrumb
				}
			}
			addedLen = unitLen
			if current != "" {
				addedLen++
			}
		}

		if current != "" {
			current += "\n" + u.text
		} else {
			current = u.text
			startsWithHeading = u.kind == kindHeading
		}
		currentLen += addedLen
	}
	flush()

	// 3. 最后一个块过小则并入前一个块（总长度允许略微超过 chunkSize）
	if len(chunks) >= 2 {
		last := chunks[len(chunks)-1]
		if len([]rune(last)) < chunkSize/2 {
			chunks[len(chunks)-2] = chunks[len(chunks)-2] + "\n" + last
			chunks = chunks[:len(chunks)-1]
		}
	}

	return chunks
}

// parseBlocks 把文本解析为语义块（围栏代码 / 表格 / 标题 / 普通行）
func parseBlocks(text string) []unit {
	lines := strings.Split(text, "\n")
	blocks := make([]unit, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		// 围栏代码块：``` 或 ~~~ 开始，找同类围栏结束
		if fence, lang, ok := fenceStart(trimmed); ok {
			j := i + 1
			var body []string
			for j < len(lines) && !isFenceEnd(strings.TrimSpace(lines[j]), fence) {
				body = append(body, lines[j])
				j++
			}
			inner := strings.Join(body, "\n")
			blocks = append(blocks, unit{
				text: fence + lang + "\n" + inner + "\n" + fence,
				kind: kindCode,
			})
			i = j // j 指向结束围栏（若存在）；不存在则 j==len(lines)
			continue
		}

		// 表格：当前行含 | 且下一行为分隔行（| --- |）
		if isTableStart(lines, i, trimmed) {
			j := i + 2
			for j < len(lines) && strings.Contains(lines[j], "|") && strings.TrimSpace(lines[j]) != "" {
				j++
			}
			blocks = append(blocks, unit{
				text: strings.Join(lines[i:j], "\n"),
				kind: kindTable,
			})
			i = j - 1
			continue
		}

		// 标题
		if level, title, ok := parseHeading(trimmed); ok {
			blocks = append(blocks, unit{text: title, kind: kindHeading, level: level})
			continue
		}

		// 普通非空行
		if trimmed != "" {
			blocks = append(blocks, unit{text: trimmed, kind: kindText})
		}
	}
	return blocks
}

// expandUnits 展开超尺寸单元：代码块带围栏硬切、表格复制表头、长段落按句/硬切
func expandUnits(blocks []unit, chunkSize int) []unit {
	units := make([]unit, 0, len(blocks))
	for _, b := range blocks {
		runeLen := len([]rune(b.text))

		switch {
		case b.kind == kindCode && runeLen > chunkSize:
			for _, p := range splitOversizeCode(b.text, chunkSize) {
				units = append(units, unit{text: p, kind: kindCode})
			}
		case b.kind == kindTable && runeLen > chunkSize:
			for _, p := range splitOversizeTable(b.text, chunkSize) {
				units = append(units, unit{text: p, kind: kindTable})
			}
		case b.kind == kindText && runeLen > chunkSize:
			for _, p := range splitLongParagraph(b.text, chunkSize) {
				units = append(units, unit{text: p, kind: kindText})
			}
		case b.kind == kindHeading && runeLen > chunkSize:
			// 极罕见的超长标题：降级为普通文本硬切
			for _, p := range splitLongParagraph(b.text, chunkSize) {
				units = append(units, unit{text: p, kind: kindText})
			}
		default:
			units = append(units, b)
		}
	}
	return units
}

// splitOversizeCode 把超长代码块切成多片，每片都带成对围栏，保证可读
func splitOversizeCode(block string, chunkSize int) []string {
	lines := strings.Split(block, "\n")
	first := lines[0] // 形如 ```go
	fence := strings.Repeat("`", fenceTickCount(first))
	if fence == "" {
		fence = "```"
	}
	// 去掉首尾围栏行，取内部代码
	bodyLines := lines[1:]
	if len(bodyLines) > 0 && isFenceLine(strings.TrimSpace(bodyLines[len(bodyLines)-1]), fence) {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}
	body := strings.Join(bodyLines, "\n")

	overhead := len([]rune(fence + "\n\n" + fence))
	budget := chunkSize - overhead
	if budget < 20 {
		budget = chunkSize / 2
	}

	pieces := make([]string, 0)
	for len([]rune(body)) > 0 {
		n := budget
		if n > len([]rune(body)) {
			n = len([]rune(body))
		}
		piece := firstRunes(body, n)
		pieces = append(pieces, first+"\n"+piece+"\n"+fence)
		body = string([]rune(body)[n:])
	}
	return pieces
}

// splitOversizeTable 把超宽表格切成多片，每片都复制「表头 + 分隔行」
func splitOversizeTable(block string, chunkSize int) []string {
	rows := strings.Split(block, "\n")
	if len(rows) < 2 {
		return splitOversizeCode("```\n"+block+"\n```", chunkSize)
	}
	header, sep := rows[0], rows[1]
	dataRows := rows[2:]
	headerLen := len([]rune(header + "\n" + sep))

	pieces := make([]string, 0)
	current := header + "\n" + sep
	currentLen := headerLen

	flush := func() {
		pieces = append(pieces, current)
		current = header + "\n" + sep
		currentLen = headerLen
	}

	for _, row := range dataRows {
		rowLen := len([]rune(row)) + 1
		if currentLen+rowLen > chunkSize && current != header+"\n"+sep {
			flush()
		}
		// 单行本身超长：截断硬放
		if current == header+"\n"+sep && rowLen > chunkSize-headerLen {
			row = firstRunes(row, chunkSize-headerLen-1)
			rowLen = len([]rune(row)) + 1
		}
		current += "\n" + row
		currentLen += rowLen
	}
	if current != header+"\n"+sep {
		flush()
	}
	if len(pieces) == 0 {
		return []string{block}
	}
	return pieces
}

// splitLongParagraph 把超过 chunkSize 的段落先按句子切，再按 rune 硬切
func splitLongParagraph(paragraph string, chunkSize int) []string {
	sentences := splitBySentence(paragraph)
	result := make([]string, 0, len(sentences))
	buf := ""
	bufLen := 0

	flush := func() {
		if buf != "" {
			result = append(result, buf)
		}
		buf = ""
		bufLen = 0
	}

	for _, s := range sentences {
		sLen := len([]rune(s))
		// 单个句子本身就超长，先硬切
		if sLen > chunkSize {
			flush()
			for len([]rune(s)) > chunkSize {
				result = append(result, firstRunes(s, chunkSize))
				s = string([]rune(s)[chunkSize:])
			}
			if s != "" {
				buf = s
				bufLen = len([]rune(s))
			}
			continue
		}

		if buf != "" && bufLen+sLen > chunkSize {
			flush()
		}
		buf += s
		bufLen += sLen
	}
	flush()
	return result
}

// splitBySentence 按句末标点切分，保留标点
func splitBySentence(text string) []string {
	runes := []rune(text)
	result := make([]string, 0, 4)
	start := 0
	for i, r := range runes {
		if strings.ContainsRune(sentenceDelimiter, r) {
			result = append(result, string(runes[start:i+1]))
			start = i + 1
		}
	}
	if start < len(runes) {
		result = append(result, string(runes[start:]))
	}
	return result
}

// updateBreadcrumb 维护「章 > 节」面包屑
func updateBreadcrumb(prev string, level int, title string) string {
	title = strings.TrimLeft(title, "# ")
	parts := []string{}
	if prev != "" {
		parts = strings.Split(prev, " > ")
	}
	// 保留更高级标题，截断同级/下级
	if level-1 < len(parts) {
		parts = parts[:level-1]
	}
	parts = append(parts, title)
	return strings.Join(parts, " > ")
}

func parseHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level < 1 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line), true
}

func fenceStart(line string) (fence, lang string, ok bool) {
	if strings.HasPrefix(line, "```") {
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	}
	if strings.HasPrefix(line, "~~~") {
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	}
	return "", "", false
}

func fenceTickCount(fenceLine string) int {
	n := 0
	for n < len(fenceLine) && (fenceLine[n] == '`' || fenceLine[n] == '~') {
		n++
	}
	return n
}

func isFenceLine(line, fence string) bool {
	if strings.HasPrefix(fence, "`") {
		return strings.HasPrefix(line, "```")
	}
	return strings.HasPrefix(line, "~~~")
}

func isFenceEnd(line, fence string) bool {
	return isFenceLine(line, fence)
}

func isTableStart(lines []string, i int, line string) bool {
	if !strings.Contains(line, "|") || i+1 >= len(lines) {
		return false
	}
	next := strings.TrimSpace(lines[i+1])
	// 分隔行形如 | --- | --- |
	if !strings.Contains(next, "|") || !strings.Contains(next, "-") {
		return false
	}
	return strings.Contains(strings.ReplaceAll(next, " ", ""), "---") ||
		strings.Count(next, "-") >= 3
}

func firstRunes(s string, n int) string {
	runes := []rune(s)
	if n > len(runes) {
		n = len(runes)
	}
	return string(runes[:n])
}

func lastRunes(s string, n int) string {
	runes := []rune(s)
	if n > len(runes) {
		n = len(runes)
	}
	return string(runes[len(runes)-n:])
}
