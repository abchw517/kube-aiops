package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
)

const wikiFileBytes = 2 << 20

func (a *identityAdmin) handleWikiFiles(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 9<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "附件批次无效或过大", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	base := wikiDocument{
		Cluster: r.PostForm.Get("cluster"), Namespace: r.PostForm.Get("namespace"),
		Environment: r.PostForm.Get("environment"), Business: r.PostForm.Get("business"), DocumentType: r.PostForm.Get("documentType"),
	}
	allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiPublish, base)
	if err != nil || !allowed {
		http.Error(w, "无权向此范围发布知识", http.StatusForbidden)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 || len(files) > 8 {
		http.Error(w, "每批请选择 1 至 8 个文件", http.StatusBadRequest)
		return
	}
	type preview struct {
		Filename string        `json:"filename"`
		Document *wikiDocument `json:"document,omitempty"`
		Error    string        `json:"error,omitempty"`
	}
	output := make([]preview, 0, len(files))
	for _, header := range files {
		row := preview{Filename: header.Filename}
		doc := base
		file, err := header.Open()
		if err == nil {
			doc.Body, err = wikiFileText(header.Filename, file)
			file.Close()
		}
		if err == nil {
			doc, err = normalizeWikiDocument(doc)
		}
		if err != nil {
			row.Error = "附件无法解析、内容为空或超过限制"
		} else {
			row.Document = &doc
		}
		output = append(output, row)
	}
	writeAdminJSON(w, output)
}

func wikiFileText(name string, input io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(input, wikiFileBytes+1))
	if err != nil || len(data) > wikiFileBytes {
		return "", errors.New("file exceeds budget")
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".txt":
		if !utf8.Valid(data) {
			return "", errors.New("invalid UTF-8")
		}
		return string(data), nil
	case ".docx":
		zipped, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", err
		}
		for _, entry := range zipped.File {
			if entry.Name != "word/document.xml" || entry.UncompressedSize64 > wikiFileBytes {
				continue
			}
			file, err := entry.Open()
			if err != nil {
				return "", err
			}
			defer file.Close()
			return wikiDOCXText(io.LimitReader(file, wikiFileBytes))
		}
	}
	return "", errors.New("unsupported document format")
}

func wikiDOCXText(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var output strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return output.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch node := token.(type) {
		case xml.StartElement:
			if node.Name.Local == "t" {
				var text string
				if err := decoder.DecodeElement(&text, &node); err != nil {
					return "", err
				}
				output.WriteString(text)
			}
			if node.Name.Local == "br" {
				output.WriteByte('\n')
			}
		case xml.EndElement:
			if node.Name.Local == "p" {
				output.WriteByte('\n')
			}
		}
		if output.Len() > 48<<10 {
			return "", errors.New("document text exceeds budget")
		}
	}
}
