package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestJoinErrorDetails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"not found", &httpStatusErr{code: http.StatusNotFound}, http.StatusNotFound, "网络不存在"},
		{"bad code", &httpStatusErr{code: http.StatusUnauthorized}, http.StatusForbidden, "配对码"},
		{"forbidden", &httpStatusErr{code: http.StatusForbidden}, http.StatusForbidden, "配对码"},
		{"duplicate", &httpStatusErr{code: http.StatusConflict}, http.StatusConflict, "已在网络内"},
		{"rate limit", &httpStatusErr{code: http.StatusTooManyRequests}, http.StatusTooManyRequests, "请求太频繁"},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "https://snet.example 不可达"},
		{"local duplicate", errors.New("本机已加入网络 ABC"), http.StatusConflict, "已在网络内"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := joinErrorDetails(tc.err, "https://snet.example")
			if code != tc.code || !strings.Contains(msg, tc.want) {
				t.Fatalf("got %d %q, want %d containing %q", code, msg, tc.code, tc.want)
			}
		})
	}
	if code, msg := joinErrorDetails(fmt.Errorf("other"), "https://snet.example"); code != http.StatusInternalServerError || !strings.Contains(msg, "加入失败") {
		t.Fatalf("unknown error = %d %q", code, msg)
	}
}
