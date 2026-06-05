package mcp

import (
	"math"
	"strings"
)

// SimpleTFIDF — 简易 TF-IDF 计算器，用来给工具描述和用户输入算相关性。
// 工具多了以后（>5 个），Agent 用这个挑出最相关的 Top-3 塞进 prompt，省 token。
// 实现就是教科书级别的 TF-IDF + 余弦相似度，没做什么优化。
type SimpleTFIDF struct {
	docs []string           // 所有工具的"名称: 描述"文本
	idf  map[string]float64 // 每个词的 IDF 值
}

// NewSimpleTFIDF — 创建实例并预计算 IDF。docs 是一批文档，每篇是一条工具描述。
func NewSimpleTFIDF(docs []string) *SimpleTFIDF {
	tfidf := &SimpleTFIDF{docs: docs}
	tfidf.calcIDF()
	return tfidf
}

// calcIDF — 统计每个词出现在多少篇文档中，算 IDF = log(总文档数/出现文档数)。
// 词在越多文档中出现，IDF 越低（说明是"的""是"这种常见词，没区分度）。
func (s *SimpleTFIDF) calcIDF() {
	docCount := len(s.docs)
	s.idf = make(map[string]float64)
	termDocCount := make(map[string]int)
	for _, doc := range s.docs {
		// 同一篇文档里重复出现的词只算一次（DF 是按文档数的）
		seen := make(map[string]bool)
		for _, word := range tokenize(doc) {
			if !seen[word] {
				termDocCount[word]++
				seen[word] = true
			}
		}
	}
	for term, count := range termDocCount {
		s.idf[term] = math.Log(float64(docCount) / float64(count))
	}
}

// Score — 算 query 和第 idx 篇文档的余弦相似度。
// 向量维度是所有词的 TF*IDF 权重。返回值 0~1，越高越相关。
func (s *SimpleTFIDF) Score(query string, idx int) float64 {
	doc := s.docs[idx]
	tfQuery := termFreq(tokenize(query))
	tfDoc := termFreq(tokenize(doc))

	score := 0.0
	normQ := 0.0
	normD := 0.0

	// 遍历 query 的词，算内积 + query 向量模长
	for term, qtf := range tfQuery {
		idf := s.idf[term]
		weightQ := qtf * idf
		normQ += weightQ * weightQ
		if dtf, ok := tfDoc[term]; ok {
			weightD := dtf * idf
			score += weightQ * weightD
		}
	}

	// 算文档向量模长（只算文档里有的词）
	for term, dtf := range tfDoc {
		idf := s.idf[term]
		weightD := dtf * idf
		normD += weightD * weightD
	}

	if normQ == 0 || normD == 0 {
		return 0
	}
	return score / (math.Sqrt(normQ) * math.Sqrt(normD))
}

// tokenize — 简单分词：转小写，按空白切。
// 生产环境应该用更靠谱的分词器（jieba 分词 or ICU）。
func tokenize(s string) []string {
	return strings.Fields(strings.ToLower(s))
}

// termFreq — 算词频，除以总词数做归一化。
// 长文档不会天然比短文档分高。
func termFreq(tokens []string) map[string]float64 {
	tf := make(map[string]float64)
	for _, t := range tokens {
		tf[t]++
	}
	sum := float64(len(tokens))
	for t := range tf {
		tf[t] /= sum
	}
	return tf
}
