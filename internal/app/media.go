package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ImageStore keeps images uploaded through the admin form. Put returns the path to store in
// the database: a relative key for R2 (joined to MEDIA_PUBLIC_URL when read) or "/media/…"
// for the local folder.
type ImageStore interface {
	Put(ctx context.Context, key, contentType string, data []byte) (string, error)
}

// LocalImageStore writes into a folder served at /media (development).
type LocalImageStore struct{ Dir string }

func (l LocalImageStore) Put(_ context.Context, key, _ string, data []byte) (string, error) {
	dst := filepath.Join(l.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return "/media/" + key, nil
}

// R2ImageStore uploads to a Cloudflare R2 bucket through its S3 API. Hosting containers lose
// their disk on every deploy, so production keeps uploads here next to the imported images.
type R2ImageStore struct {
	AccountID, AccessKeyID, SecretAccessKey, Bucket string
	Prefix                                          string // e.g. "master/uploads"
	Client                                          *http.Client
}

func (s R2ImageStore) Put(ctx context.Context, key, contentType string, data []byte) (string, error) {
	objectKey := strings.Trim(s.Prefix+"/"+key, "/")
	u := &url.URL{Scheme: "https", Host: s.AccountID + ".r2.cloudflarestorage.com", Path: "/" + s.Bucket + "/" + objectKey}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
	signV4(req, data, s.AccessKeyID, s.SecretAccessKey, "auto", "s3", time.Now())
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("r2 upload %s: %s: %s", objectKey, res.Status, body)
	}
	return objectKey, nil
}

// signV4 adds AWS Signature Version 4 headers (as used by S3 and R2) to req, signing every
// header already set plus host, x-amz-date and x-amz-content-sha256.
func signV4(req *http.Request, payload []byte, accessKey, secretKey, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))

	key := hmacSHA256([]byte("AWS4"+secretKey), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape percent-encodes everything except the unreserved characters A-Z a-z 0-9 - _ . ~
func awsEscape(s string) string {
	return strings.NewReplacer("+", "%20", "%7E", "~").Replace(url.QueryEscape(s))
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
