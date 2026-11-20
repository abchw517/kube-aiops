package wiki

import "testing"

func TestChapterAnswerRejectsInventedOrDuplicateKeys(t *testing.T) {
	doc := Document{ID: "sample", Title: "排障手册", Revision: 4, DocumentType: "排障手册", Metadata: Metadata{Sections: map[string]string{
		"checks": "读取状态", "recovery": "确认恢复", "invented": "不应送给模型",
	}}}
	excerpts := askEvidence([]Citation{{ID: doc.ID, Revision: doc.Revision, chapters: chapterEvidence(doc)}})
	if len(excerpts) != 2 || excerpts[0].Section != "checks" || hasWikiSection(doc, "invented") || hasWikiSection(doc, "body") {
		t.Fatalf("incorrect chapter evidence: %+v", excerpts)
	}
	answer, cites, ok := decodeChapterAnswer(`{"answer":"确认恢复","citations":["K2","unknown","K2"]}`, excerpts)
	if !ok || answer != "确认恢复" || len(cites) != 1 || cites[0].Revision != 4 || cites[0].Section != "recovery" || cites[0].Excerpt != "" {
		t.Fatalf("unverified answer: %q %+v", answer, cites)
	}
	if text, cites, ok := decodeChapterAnswer(`{"answer":"不应展示","citations":["unknown"]}`, excerpts); !ok || len(cites) != 0 || text == "不应展示" {
		t.Fatal("unsupported answer exposed")
	}
}
