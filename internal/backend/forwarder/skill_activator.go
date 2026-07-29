// skill_activator.go 实现内置技能的「调用链稀疏激活」：
// 不再每个请求全量注入所有技能，而是用 BM25 对当前请求文本与各技能 description
// 做语义相关性打分，只激活 Top-K（默认 3）相关技能；元技能 find-skills 始终常驻。
//
// 子代理请求会重打分，并以父会话最近一次激活集作保底候选，保证调用链上下文一致性。
//
// 打分确定性：相同 queryText + 相同技能集 + 相同父集 -> 相同激活集，保证同 turn
// 重试/重编译的 prefix 稳定（见 prefix-cache-stability 约束）。
package forwarder

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/wangbin/jiebago"
)

// 稀疏激活参数。
const (
	activatedSkillsTopK        = 3  // 每次最多激活的打分技能数（不含常驻元技能）
	activatedSkillsThreshold   = 0.0 // BM25 得分阈值；0 表示只要有共享词即候选
	activatedSkillsMaxInject   = 4  // 最终注入上限（含常驻元技能）
	metaSkillAlwaysOnName      = "find-skills"
)

// bm25 经验参数。
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// skillTokenizer 是进程级中文分词器（jiebago 词典只需加载一次）。
// 加载失败时退化为纯英文/规则分词，不阻断激活。
var (
	skillTokenizer     *jiebago.Segmenter
	skillTokenizerOnce sync.Once
	skillTokenizerOK   bool
)

func initSkillTokenizer() {
	skillTokenizerOnce.Do(func() {
		seg := &jiebago.Segmenter{}
		// jiebago 需要从文件加载词典。把内置领域词典写入临时文件再加载，
		// 避免依赖模块缓存里的 5MB dict.txt（体积大且路径不稳定）。
		// 加载失败则退化，tokenize 走纯英文/规则路径。
		path, err := writeEmbeddedSkillDict()
		if err != nil {
			skillTokenizerOK = false
			return
		}
		defer os.Remove(path)
		if err := seg.LoadDictionary(path); err != nil {
			skillTokenizerOK = false
			return
		}
		skillTokenizer = seg
		skillTokenizerOK = true
	})
}

// writeEmbeddedSkillDict 把 builtinSkillDict 内容写入临时文件，返回其路径。
// 内置词典只覆盖技能领域常用词（调试/计划/测试/技能/创建 等），体积小、可嵌入。
func writeEmbeddedSkillDict() (string, error) {
	f, err := os.CreateTemp("", "skill-dict-*.txt")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(builtinSkillDict); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return filepath.Join(filepath.Dir(f.Name()), filepath.Base(f.Name())), nil
}

// builtinSkillDict 是覆盖技能激活领域常用词的精简中文词典（jiebago dict.txt 格式：
// 每行「词 频率 词性」，频率为相对值）。仅为 BM25 打分提供分词，不追求完整覆盖。
const builtinSkillDict = `技能 1000 n
技巧 500 n
能力 500 n
创建 800 v
编写 600 v
实现 800 v
修改 600 v
编辑 500 v
调试 1000 v
修复 600 v
错误 800 n
故障 500 n
失败 500 n
测试 1000 v
验证 800 v
检查 600 v
审查 500 v
计划 800 n
规划 500 v
设计 800 v
文档 800 n
说明书 200 n
规范 600 n
规则 600 n
代码 1000 n
编程 600 v
开发 800 v
重构 500 v
分支 500 n
合并 400 v
提交 600 v
发布 600 v
版本 600 n
技能资源 400 n
技能库 400 n
提示 600 n
注入 500 v
激活 600 v
上下文 600 n
会话 600 n
对话 600 n
任务 800 n
工作流 500 n
流程 600 n
步骤 500 n
代理 600 n
子代理 400 n
搜索 600 v
查找 600 v
定位 500 v
分析 600 v
诊断 600 v
排查 500 v
日志 600 n
记录 600 n
统计 600 n
指标 500 n
监控 500 v
配置 800 n
设置 600 n
选项 500 n
国际化 400 n
翻译 500 n
语言 500 n
前端 600 n
后端 600 n
接口 600 n
模型 800 n
供应商 500 n
价格 500 n
花费 400 n
估算 500 v
缓存 600 n
窗口 500 n
浮窗 400 n
浏览器 500 n
预览 500 n
路由 500 n
页面 500 n
组件 500 n
按钮 400 n
样式 400 n
布局 400 n
数据库 500 n
查询 500 v
索引 500 n
权限 500 n
认证 500 n
加密 500 n
安全 600 n
性能 600 n
优化 600 v
缓存命中 300 n
实时 500 adj
数据 800 n
文件 800 n
目录 500 n
路径 500 n
脚本 500 n
构建 600 v
编译 500 v
部署 500 v
环境 600 n
依赖 500 n
包 400 n
模块 500 n
函数 500 n
变量 400 n
类型 500 n
结构 500 n
接口 600 n
协议 500 n
请求 600 n
响应 500 n
异步 400 adj
队列 500 n
锁 400 n
并发 500 n
线程 400 n
进程 400 n
内存 500 n
磁盘 400 n
网络 500 n
连接 500 n
超时 400 n
重试 400 v
取消 400 v
中断 400 v
恢复 400 v
迁移 400 v
升级 400 v
降级 400 v
回退 400 v
兜底 400 v
保底 400 n
候选 400 n
排序 400 v
去重 300 v
过滤 400 v
映射 400 n
转换 400 v
解析 500 v
序列化 400 v
持久化 400 v
快照 400 n
检查点 300 n
上下文窗口 200 n
稀疏 300 adj
激活 600 v
相关性 400 n
相似度 400 n
向量 400 n
分词 400 n
停用词 200 n
阈值 400 n
`

// tokenize 把文本切分为小写词项（去停用词与标点）。
// 中文走 jiebago，英文/数字走空格与字符边界切分；二者结果合并。
func tokenize(text string) []string {
	initSkillTokenizer()
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	tokens := make([]string, 0, 16)
	seen := make(map[string]struct{}, 16)
	add := func(raw string) {
		w := strings.TrimSpace(raw)
		if w == "" || isStopword(w) || !hasWordChar(w) {
			return
		}
		if _, ok := seen[w]; ok {
			return
		}
		seen[w] = struct{}{}
		tokens = append(tokens, w)
	}

	// 中文分词（若可用）。jiebago 对英文也会按空格切，但英文边界由下方规则兜底。
	if skillTokenizerOK && skillTokenizer != nil && containsCJK(text) {
		for w := range skillTokenizer.Cut(text, true) {
			add(w)
		}
	}

	// 英文/数字/符号切分：按非字母数字边界拆分。
	addLatinTokens(text, add)

	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

// addLatinTokens 按连续拉丁字母/数字段切分并通过 add 收集。
func addLatinTokens(text string, add func(string)) {
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			add(sb.String())
			sb.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) && r < 0x4E00: // 拉丁字母（排除 CJK 表意）
			sb.WriteRune(r)
		case unicode.IsDigit(r):
			sb.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
}

// containsCJK 报告文本是否含 CJK 字符。
func containsCJK(text string) bool {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// isStopword 报告词项是否为应忽略的常见停用词/单字符噪声。
func isStopword(w string) bool {
	switch w {
	case "the", "a", "an", "is", "are", "of", "to", "in", "on", "for", "and", "or",
		"with", "when", "use", "using", "this", "that", "it", "as", "by", "be",
		"any", "before", "after", "not", "you", "they", "their",
		"的", "了", "在", "是", "和", "与", "用", "为", "对", "把", "被", "让", "使",
		"你", "我", "他", "她", "它", "们", "个", "这", "那", "帮", "需要", "时", "当",
		"新", "更":
		return true
	}
	// 单字符拉丁词多为噪声。
	if utf8.RuneCountInString(w) == 1 {
		r, _ := utf8.DecodeRuneInString(w)
		if r < 0x4E00 {
			return true
		}
	}
	return false
}

// hasWordChar 报告词项是否含至少一个字母、数字或 CJK 字符；
// 用于过滤纯标点/符号噪声（如 "，"、"。"、"·"）。
func hasWordChar(w string) bool {
	for _, r := range w {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// skillDoc 是一个技能的 BM25 文档表示。
type skillDoc struct {
	skill   GlobalSkill
	terms   []string        // 去停用词后的词项
	termFreq map[string]int // 词项 -> 出现次数
	docLen  int
}

// skillIndex 是一次构建的 BM25 倒排索引快照。
type skillIndex struct {
	docs      []skillDoc
	termDoc   map[string]int      // 词项 -> 出现该词项的文档数（df）
	avgDocLen float64
	nameToDoc map[string]int      // 技能名(小写) -> docs 索引，保证打分时定位正确文档
	fingerprint string            // 索引对应技能集的内容指纹
}

// SkillActivator 负责按请求文本稀疏激活技能。
// 它持有 SkillStore 以扫描技能并（可选）读写会话父子激活集。
type SkillActivator struct {
	store *SkillStore

	mu    sync.Mutex
	index *skillIndex
}

// NewSkillActivator 创建激活器。store 不可为 nil。
func NewSkillActivator(store *SkillStore) *SkillActivator {
	return &SkillActivator{store: store}
}

// Activate 返回应注入的技能列表（顺序：元技能优先，再按相关性降序）。
// parentActivated 是父会话最近激活的技能名集合（子代理保底用），可为空。
// 结果含常驻元技能 find-skills（若存在）+ Top-K 打分技能，去重后截断到 activatedSkillsMaxInject。
func (a *SkillActivator) Activate(queryText string, parentActivated []string) []GlobalSkill {
	if a == nil || a.store == nil {
		return nil
	}
	skills, err := a.store.Scan()
	if err != nil || len(skills) == 0 {
		return nil
	}

	idx := a.ensureIndex(skills)

	// 分离常驻元技能与其余技能。
	var meta []GlobalSkill
	others := make([]GlobalSkill, 0, len(skills))
	metaNames := make(map[string]struct{})
	for _, s := range skills {
		if strings.EqualFold(s.Name, metaSkillAlwaysOnName) {
			meta = append(meta, s)
			metaNames[strings.ToLower(s.Name)] = struct{}{}
		} else {
			others = append(others, s)
		}
	}

	qTerms := tokenize(queryText)
	parentSet := normalizeParentNames(parentActivated)

	// 打分（仅对 others）。
	type scored struct {
		skill GlobalSkill
		score float64
		fromParent bool
	}
	results := make([]scored, 0, len(others))
	for _, s := range others {
		docIdx, ok := idx.nameToDoc[strings.ToLower(s.Name)]
		if !ok {
			continue
		}
		sc := bm25Score(idx, docIdx, qTerms)
		isParent := false
		if _, ok := parentSet[strings.ToLower(s.Name)]; ok {
			isParent = true
			// 父集保底：即使打分为 0 也作为候选，给一个极小正值以保证入选排序。
			if sc <= activatedSkillsThreshold {
				sc = 1e-9
			}
		}
		if sc > activatedSkillsThreshold || isParent {
			results = append(results, scored{skill: s, score: sc, fromParent: isParent})
		}
	}

	// 排序：分数降序；同分时父集优先；再按名称稳定排序。
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].fromParent != results[j].fromParent {
			return results[i].fromParent
		}
		return results[i].skill.Name < results[j].skill.Name
	})

	topK := activatedSkillsTopK
	if topK > len(results) {
		topK = len(results)
	}

	// 合并：元技能 + Top-K，去重。
	seen := make(map[string]struct{}, topK+len(meta)+1)
	activated := make([]GlobalSkill, 0, topK+len(meta)+1)
	for _, s := range meta {
		key := strings.ToLower(s.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		activated = append(activated, s)
	}
	for k := 0; k < topK; k++ {
		key := strings.ToLower(results[k].skill.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		activated = append(activated, results[k].skill)
	}

	// 最终截断到上限（含元技能）。
	if len(activated) > activatedSkillsMaxInject {
		activated = activated[:activatedSkillsMaxInject]
	}
	return activated
}

// bm25Score 计算查询词项对第 docIndex 个文档的 BM25 得分。
func bm25Score(idx *skillIndex, docIndex int, qTerms []string) float64 {
	if idx == nil || docIndex < 0 || docIndex >= len(idx.docs) {
		return 0
	}
	doc := idx.docs[docIndex]
	if len(qTerms) == 0 || doc.docLen == 0 {
		return 0
	}
	n := len(idx.docs)
	var score float64
	qSeen := make(map[string]struct{}, len(qTerms))
	for _, t := range qTerms {
		if _, ok := qSeen[t]; ok {
			continue
		}
		qSeen[t] = struct{}{}
		tf := doc.termFreq[t]
		if tf == 0 {
			continue
		}
		df := idx.termDoc[t]
		if df == 0 {
			continue
		}
		idf := math.Log((float64(n)-float64(df)+0.5)/(float64(df)+0.5) + 1)
		denom := float64(tf) + bm25K1*(1-bm25B+bm25B*float64(doc.docLen)/idx.avgDocLen)
		if denom == 0 {
			continue
		}
		score += idf * (float64(tf)*(bm25K1+1)) / denom
	}
	return score
}

// ensureIndex 返回与 skills 对应的索引；技能集变化时按指纹重建。
func (a *SkillActivator) ensureIndex(skills []GlobalSkill) *skillIndex {
	fp := indexFingerprint(skills)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.index != nil && a.index.fingerprint == fp {
		return a.index
	}
	a.index = buildIndex(skills, fp)
	return a.index
}

// buildIndex 为给定技能集构建 BM25 倒排索引。
// 文档文本 = 技能 Name + Description 合并分词，让英文技能名也能参与匹配。
func buildIndex(skills []GlobalSkill, fp string) *skillIndex {
	docs := make([]skillDoc, 0, len(skills))
	termDoc := make(map[string]int)
	nameToDoc := make(map[string]int, len(skills))
	var totalLen int
	for i, s := range skills {
		// 合并 Name + Description 作为文档，提升英文技能名（如 debugging/i18n）的可匹配性。
		docText := strings.TrimSpace(s.Name) + " " + strings.TrimSpace(s.Description)
		terms := tokenize(docText)
		tf := make(map[string]int, len(terms))
		for _, t := range terms {
			tf[t]++
		}
		docs = append(docs, skillDoc{
			skill:    s,
			terms:    terms,
			termFreq: tf,
			docLen:   len(terms),
		})
		nameToDoc[strings.ToLower(s.Name)] = i
		totalLen += len(terms)
		for t := range tf {
			termDoc[t]++
		}
	}
	avg := 0.0
	if len(docs) > 0 {
		avg = float64(totalLen) / float64(len(docs))
	}
	return &skillIndex{
		docs:        docs,
		termDoc:     termDoc,
		avgDocLen:   avg,
		nameToDoc:   nameToDoc,
		fingerprint: fp,
	}
}

// indexFingerprint 用技能名+description 拼接生成指纹，用于检测技能集变化。
func indexFingerprint(skills []GlobalSkill) string {
	var sb strings.Builder
	for _, s := range skills {
		sb.WriteString(s.Name)
		sb.WriteByte('|')
		sb.WriteString(s.Description)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// normalizeParentNames 把父激活名列表归一为小写集合。
func normalizeParentNames(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			set[n] = struct{}{}
		}
	}
	return set
}
