package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxArticleChars = 20000
const maxArticleBytes = maxArticleChars * 4

// Validate the Word package without extracting its contents to disk.
func validateDOCX(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return errors.New("请选择有效的 Word .docx 文档")
	}
	defer archive.Close()
	if len(archive.File) > 2048 {
		return errors.New("Word 文档内容过于复杂，请精简后上传")
	}
	var document *zip.File
	var types, relations bool
	var total uint64
	seen := map[string]bool{}
	for _, file := range archive.File {
		if seen[file.Name] || file.Flags&1 != 0 || file.UncompressedSize64 > 100<<20 {
			return errors.New("Word 文档结构无效或内容过大")
		}
		seen[file.Name] = true
		total += file.UncompressedSize64
		if total > 100<<20 {
			return errors.New("Word 文档解压后超过 100 MB，请精简后上传")
		}
		switch file.Name {
		case "[Content_Types].xml":
			types = true
		case "_rels/.rels":
			relations = true
		case "word/document.xml":
			document = file
		}
	}
	if !types || !relations || document == nil || document.UncompressedSize64 > 4<<20 {
		return errors.New("请选择有效的 .docx 文档，正文 XML 不能超过 4 MB")
	}
	reader, err := document.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(io.LimitReader(reader, (4<<20)+1))
	root := false
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) && root && depth == 0 {
			return nil
		}
		if err != nil {
			return errors.New("Word 正文损坏，请重新保存为 .docx 后上传")
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if root || token.Name.Local != "document" || (token.Name.Space != "http://schemas.openxmlformats.org/wordprocessingml/2006/main" && token.Name.Space != "http://purl.oclc.org/ooxml/wordprocessingml/main") {
					return errors.New("Word 正文格式无效")
				}
				root = true
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

func stageArticle(stage, text, title string, studentID int64) (stagedWork, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxArticleChars {
		return stagedWork{}, errors.New("文章正文最多 20000 字，请精简后提交或上传 .docx")
	}
	filename := randomID() + ".txt"
	path := filepath.Join(stage, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return stagedWork{}, err
	}
	_, err = file.WriteString(text)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return stagedWork{}, err
	}
	name := "我的文章.txt"
	if title != "" {
		name = exportComponent(title) + ".txt"
	}
	hash := sha256.Sum256([]byte(text))
	return stagedWork{Work: Work{StudentID: studentID, Filename: filename, OriginalName: name}, Path: path, Hash: hex.EncodeToString(hash[:])}, nil
}

func (a *App) articleText(work Work) string {
	file, err := a.files.Open(work.Filename)
	if err != nil {
		return "文章暂时无法读取，请联系老师。"
	}
	defer file.Close()
	text, err := io.ReadAll(io.LimitReader(file, maxArticleBytes+1))
	if err != nil || len(text) > maxArticleBytes || !utf8.Valid(text) {
		return "文章暂时无法读取，请联系老师。"
	}
	return string(text)
}
