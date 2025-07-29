package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/go-chi/chi"
	"github.com/h2non/filetype"
	"github.com/subosito/gozaru"
	"gopkg.in/yaml.v3"
)

var rootUploadPath = "/app/files/"

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
}

var maxBytesReader *http.MaxBytesError

func serveFile(w http.ResponseWriter, r *http.Request) {
	fileId := chi.URLParam(r, "fileid")

	metadataFilePath := filepath.Join(rootUploadPath, fileId[:2], fileId[2:4], fileId+".yaml")
	metadataFile, err := os.ReadFile(metadataFilePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	var metaData map[string]any
	if err := yaml.Unmarshal(metadataFile, &metaData); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if delete := metaData["delete"].(bool); delete {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fileHash := metaData["hash"].(string)
	filePath := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4], fileHash)
	originalFileName := metaData["filename"].(string)

	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(originalFileName))
	http.ServeFile(w, r, filePath)
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(settingConfig.MaxFileSize)<<20)

	file, handler, err := r.FormFile("file")
	if err != nil {
		if errors.As(err, &maxBytesReader) {
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := os.MkdirAll(rootUploadPath, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	head := make([]byte, 512)
	file.Read(head)

	t, _ := filetype.Match(head)

	file.Seek(0, io.SeekStart)
	fileHash, err := generatedFileHash(file)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	hashSubDir := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4])
	if err := os.MkdirAll(hashSubDir, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var isDuplicateFile bool
	testISDuplicateFilePath := filepath.Join(hashSubDir, fileHash)
	_, err = os.Stat(testISDuplicateFilePath)
	if err == nil { //|| !os.IsNotExist(err)
		isDuplicateFile = true
	}

	if !isDuplicateFile {
		destPath := filepath.Join(hashSubDir, fileHash)

		file.Seek(0, io.SeekStart)
		destFile, err := os.Create(destPath)
		if err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, file); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}

	id := generatedRandomID(20)
	if id == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	yamlFileDir := filepath.Join(rootUploadPath, id[:2], id[2:4])
	if err := os.MkdirAll(yamlFileDir, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	safeFilename := gozaru.Sanitize(handler.Filename)

	fileMetadata := map[string]any{
		"id":       id,
		"filename": safeFilename,
		"hash":     fileHash,
		"type":     t.MIME.Type,
		"delete":   false,
	}
	metadataFilePath := filepath.Join(rootUploadPath, id[:2], id[2:4], id+".yaml")
	metadataFile, err := os.Create(metadataFilePath)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer metadataFile.Close()

	yamlData, err := yaml.Marshal(fileMetadata)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	metadataFile.Write(yamlData)

	fileUrl := "/api/files/" + id

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FileResponse{
		URL:      fileUrl,
		Filename: handler.Filename,
		FileType: t.MIME.Type,
	})
}

func generatedFileHash(file io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func generatedRandomID(len int) string {
	b := make([]byte, len)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}

	return hex.EncodeToString(b)
}

// TODO: Image size limitation
func getFavicon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := getChannelDetails(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	logoUrl := c["logoUrl"]
	if logoUrl == "" {
		logoUrl = "assets/favicon.ico"
	}
	fileId := path.Base(logoUrl)

	metadataFilePath := filepath.Join(rootUploadPath, fileId[:2], fileId[2:4], fileId+".yaml")
	metadataFile, err := os.ReadFile(metadataFilePath)
	if err != nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	var metaData map[string]any
	if err := yaml.Unmarshal(metadataFile, &metaData); err != nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	if delete := metaData["delete"].(bool); delete {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	fileHash := metaData["hash"].(string)
	filePath := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4], fileHash)

	http.ServeFile(w, r, filePath)
}
