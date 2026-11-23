package client

import (
	"strings"
	"testing"
)

func TestNetworkGoneNoticeIsExposed(t *testing.T) {
	d := NewDaemonAt(&Config{}, "")

	d.mu.Lock()
	d.setNetworkGoneNoticeLocked("HomeNET")
	first := d.notice
	d.setNetworkGoneNoticeLocked("")
	second := d.notice
	seq := d.noticeSeq
	d.mu.Unlock()

	if seq != 2 {
		t.Fatalf("notice seq = %d, want 2", seq)
	}
	if !strings.Contains(first, "HomeNET") {
		t.Fatalf("first notice = %q, want network name", first)
	}
	if !strings.Contains(second, "该网络") || first == second {
		t.Fatalf("second notice = %q, want fallback text", second)
	}

	status, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	notice, ok := status["notice"].(map[string]any)
	if !ok || notice["text"] != second {
		t.Fatalf("status notice = %#v, want %q", status["notice"], second)
	}
}
