package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

func healthcheck(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid healthcheck port")
	}
	client := &http.Client{
		Timeout:       3 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/health")
	if err != nil {
		return errors.New("application healthcheck failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("application is not healthy")
	}
	return nil
}
