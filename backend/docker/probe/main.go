// Local-only HTTP probe for disposable container integration tests. Not shipped.
package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	var input struct {
		Method, Path, Token string
		Body                json.RawMessage
		Upload              bool
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		log.Fatal(err)
	}
	if !strings.HasPrefix(input.Path, "/") || strings.HasPrefix(input.Path, "//") {
		log.Fatal("invalid local path")
	}
	body := bytes.NewBuffer(input.Body)
	contentType := "application/json"
	if input.Upload {
		body.Reset()
		w := multipart.NewWriter(body)
		file, err := w.CreateFormFile("file", "docker-fixture.png")
		if err != nil {
			log.Fatal(err)
		}
		if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			log.Fatal(err)
		}
		contentType = w.FormDataContentType()
		if err = w.Close(); err != nil {
			log.Fatal(err)
		}
	}
	req, err := http.NewRequest(input.Method, "http://127.0.0.1:8080"+input.Path, body)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	if input.Token != "" {
		req.Header.Set("Authorization", "Bearer "+input.Token)
	}
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		log.Fatal(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": resp.StatusCode, "body": string(data)}); err != nil {
		log.Fatal(err)
	}
}
