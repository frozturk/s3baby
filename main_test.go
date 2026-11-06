package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(&server{root: t.TempDir()})
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func read(t *testing.T, res *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPutThenGetReturnsTheBodyAndContentType(t *testing.T) {
	srv := newServer(t)
	url := srv.URL + "/bucket/a/b.txt?X-Amz-Signature=ignored"

	put := do(t, http.MethodPut, url, "hello", map[string]string{"Content-Type": "text/plain"})
	if put.StatusCode != http.StatusOK || put.Header.Get("ETag") == "" {
		t.Fatalf("put: %d etag=%q", put.StatusCode, put.Header.Get("ETag"))
	}

	get := do(t, http.MethodGet, url, "", nil)
	if body := read(t, get); get.StatusCode != http.StatusOK || body != "hello" || get.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("get: %d %q %q", get.StatusCode, body, get.Header.Get("Content-Type"))
	}
}

func TestPutDecodesAwsChunkedBodies(t *testing.T) {
	srv := newServer(t)
	url := srv.URL + "/bucket/chunked.txt"
	body := "5;chunk-signature=abc\r\nhello\r\n6;chunk-signature=def\r\n world\r\n0;chunk-signature=ghi\r\n\r\n"

	do(t, http.MethodPut, url, body, map[string]string{"X-Amz-Content-Sha256": "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"})

	if got := read(t, do(t, http.MethodGet, url, "", nil)); got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestMissingKeyIsNoSuchKey(t *testing.T) {
	res := do(t, http.MethodGet, newServer(t).URL+"/bucket/nope", "", nil)
	if body := read(t, res); res.StatusCode != http.StatusNotFound || !strings.Contains(body, "NoSuchKey") {
		t.Fatalf("got %d %q", res.StatusCode, body)
	}
}

func TestDeleteRemovesTheObject(t *testing.T) {
	srv := newServer(t)
	url := srv.URL + "/bucket/gone.txt"
	do(t, http.MethodPut, url, "x", nil)

	if res := do(t, http.MethodDelete, url, "", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res := do(t, http.MethodGet, url, "", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: %d", res.StatusCode)
	}
}

func TestPreflightAllowsTheRequestingOrigin(t *testing.T) {
	res := do(t, http.MethodOptions, newServer(t).URL+"/bucket/x", "", map[string]string{
		"Origin":                         "http://localhost:5173",
		"Access-Control-Request-Headers": "content-type",
	})
	if res.StatusCode != http.StatusOK || res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("got %d %q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestPathsCannotLeaveTheDataFolder(t *testing.T) {
	s := &server{root: t.TempDir()}
	for _, path := range []string{"/bucket/../../evil.txt", "/../evil.txt", "/.meta/x", "/"} {
		if _, _, ok := s.split(path); ok {
			t.Fatalf("%s was accepted", path)
		}
	}
	if _, _, ok := s.split("/bucket/a/b.txt"); !ok {
		t.Fatal("a normal key was rejected")
	}
}
