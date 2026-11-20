package wiki

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

var wikiSections = map[string][]string{
	"排障手册": {"conditions", "signals", "checks", "recovery", "escalation"},
	"故障复盘": {"impact", "evidence", "conclusion", "recovery"},
	"操作规程": {"conditions", "steps", "rollback", "recovery"},
	"常见问题": {"question", "answer", "conditions"},
}

func hasWikiSection(doc Document, key string) bool {
	for _, section := range wikiSections[doc.DocumentType] {
		if key == section && strings.TrimSpace(doc.Metadata.Sections[key]) != "" {
			return true
		}
	}
	return key == "body" && len(chapterEvidence(doc)) == 0 && strings.TrimSpace(doc.Body) != ""
}

func chapterEvidence(doc Document) []Citation {
	var result []Citation
	for _, section := range wikiSections[doc.DocumentType] {
		if text := strings.TrimSpace(doc.Metadata.Sections[section]); text != "" {
			result = append(result, Citation{ID: doc.ID, Title: doc.Title, Revision: doc.Revision, Section: section,
				Excerpt: sanitizer.WikiText(text, 900)})
		}
	}
	return result
}

type wikiEvidenceExcerpt struct {
	Key string `json:"key"`
	Citation
}

func askEvidence(docs []Citation) []wikiEvidenceExcerpt {
	out := []wikiEvidenceExcerpt{}
	for _, doc := range docs {
		chapters := doc.chapters
		if len(chapters) == 0 {
			doc.Section = "body"
			chapters = []Citation{doc}
		}
		for _, chapter := range chapters {
			out = append(out, wikiEvidenceExcerpt{Key: "K" + strconv.Itoa(len(out)+1), Citation: chapter})
		}
	}
	return out
}

func decodeChapterAnswer(raw string, excerpts []wikiEvidenceExcerpt) (string, []Citation, bool) {
	var output struct {
		Answer    string   `json:"answer"`
		Citations []string `json:"citations"`
	}
	out := []Citation{}
	raw = strings.TrimSpace(raw)
	if first, payload, ok := strings.Cut(raw, "\n"); ok && (first == "```json" || first == "```") && strings.HasSuffix(payload, "\n```") {
		raw = strings.TrimSpace(strings.TrimSuffix(payload, "\n```"))
	}
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &output) != nil || strings.TrimSpace(output.Answer) == "" || len(output.Citations) > 100 {
		return "", out, false
	}
	index := make(map[string]Citation, len(excerpts))
	for _, excerpt := range excerpts {
		index[excerpt.Key] = excerpt.Citation
	}
	for _, key := range output.Citations {
		citation, ok := index[key]
		if !ok {
			continue
		}
		delete(index, key)
		citation.Excerpt = ""
		out = append(out, citation)
	}
	if len(out) == 0 {
		return "未找到可验证的章节依据。请缩小范围后重试，或直接搜索知识库。", out, true
	}
	return sanitizer.WikiText(output.Answer, 8192), out, true
}
