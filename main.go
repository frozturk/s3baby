package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type server struct {
	root string
}

type meta struct {
	ContentType string `json:"content_type"`
	ETag        string `json:"etag"`
}

func main() {
	root, err := filepath.Abs(env("S3BABY_DATA", "data"))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	addr := env("S3BABY_HOST", "127.0.0.1") + ":" + env("S3BABY_PORT", "59000")
	log.Printf("s3baby on http://%s, storing in %s", addr, root)
	log.Fatal(http.ListenAndServe(addr, &server{root: root}))
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.Method, r.URL.Path)
	allowCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	bucket, key, ok := s.split(r.URL.Path)
	if !ok {
		s3Error(w, http.StatusBadRequest, "InvalidRequest", "Invalid bucket or key.")
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.put(w, r, bucket, key)
	case http.MethodGet, http.MethodHead:
		s.get(w, r, bucket, key)
	case http.MethodDelete:
		s.delete(w, bucket, key)
	default:
		s3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.")
	}
}

func allowCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	headers := r.Header.Get("Access-Control-Request-Headers")
	if headers == "" {
		headers = "*"
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", "GET, PUT, HEAD, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", headers)
	h.Set("Access-Control-Expose-Headers", "ETag")
}

func (s *server) split(path string) (string, string, bool) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if bucket == "" || strings.HasPrefix(bucket, ".") {
		return "", "", false
	}
	if key != "" && !inside(s.root, s.objectPath(bucket, key)) {
		return "", "", false
	}
	return bucket, key, true
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func (s *server) objectPath(bucket, key string) string {
	return filepath.Join(s.root, bucket, filepath.FromSlash(key))
}

func (s *server) metaPath(bucket, key string) string {
	return filepath.Join(s.root, ".meta", bucket, filepath.FromSlash(key)+".json")
}

func (s *server) put(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if key == "" {
		if err := os.MkdirAll(filepath.Join(s.root, bucket), 0o755); err != nil {
			s3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	var body io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING") || strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		body = awsChunked(bufio.NewReader(r.Body))
	}

	path := s.objectPath(bucket, key)
	etag, err := writeFile(path, body)
	if err != nil {
		s3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := writeMeta(s.metaPath(bucket, key), meta{ContentType: contentType, ETag: etag}); err != nil {
		s3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func writeFile(path string, body io.Reader) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	hash := md5.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return `"` + hex.EncodeToString(hash.Sum(nil)) + `"`, nil
}

func writeMeta(path string, m meta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func awsChunked(br *bufio.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
			size, err := strconv.ParseInt(sizeHex, 16, 64)
			if err != nil {
				pw.CloseWithError(fmt.Errorf("bad aws-chunked size %q", sizeHex))
				return
			}
			if size == 0 {
				pw.Close()
				return
			}
			if _, err := io.CopyN(pw, br, size); err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := br.Discard(2); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}

func (s *server) get(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if key == "" {
		s3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}
	f, err := os.Open(s.objectPath(bucket, key))
	if err != nil {
		s3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}

	m := meta{ContentType: "application/octet-stream"}
	if data, err := os.ReadFile(s.metaPath(bucket, key)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	w.Header().Set("Content-Type", m.ContentType)
	if m.ETag != "" {
		w.Header().Set("ETag", m.ETag)
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *server) delete(w http.ResponseWriter, bucket, key string) {
	if key != "" {
		for _, path := range []string{s.objectPath(bucket, key), s.metaPath(bucket, key)} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func s3Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, message)
}
