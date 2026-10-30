package recruitmentmail

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNoticeParsingKeepsAmbiguousTimesAndDoesNotGuessMissingYear(t *testing.T) {
	p, e := Parse("合成公司面试改期\n原时间 2026-11-02 14:00，新时间 2026-11-03 16:30。申请编号 CT-123", "text")
	if e != nil || p.Kind != "RESCHEDULE" || p.Reference != "CT-123" || p.ScheduledAt != nil || len(p.TimeCandidates) != 2 {
		t.Fatal(p, e)
	}
	p, e = Parse("合成公司笔试通知\n时间 2026年11月02日 14:00", "text")
	if e != nil || p.ScheduledAt == nil || p.ScheduledAt.Format("15:04") != "06:00" {
		t.Fatal(p, e)
	}
	p, _ = Parse("面试 11月02日 14:00", "text")
	if p.ScheduledAt != nil {
		t.Fatal("guessed year")
	}
	p, _ = Parse("面试 2026-02-30 14:00", "text")
	if p.ScheduledAt != nil {
		t.Fatal("normalized invalid date")
	}
}
func TestEMLTransferEncodingMultipartHTMLAndLimits(t *testing.T) {
	body := "合成公司面试通知 2026-11-02T14:00:00+08:00"
	eml := "Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte("面试通知")) + "?=\r\nMessage-ID: <fixture@example.invalid>\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(body)) + "\r\n--x\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=ignore.txt\r\n\r\n附件里的 2026-11-04 16:00\r\n--x--\r\n"
	p, e := Parse(eml, "eml")
	if e != nil || p.Subject != "面试通知" || p.MessageID != "<fixture@example.invalid>" || p.Text != body || len(p.TimeCandidates) != 1 {
		t.Fatal(p, e)
	}
	p, e = Parse("Subject: Notice\r\nContent-Type: text/html\r\n\r\n<p>面试通知</p><script>fetch('secret')</script><img src='https://example.invalid'>", "eml")
	if e != nil || strings.Contains(p.Text, "secret") || strings.Contains(p.Text, "img") {
		t.Fatal(p, e)
	}
	for _, c := range []string{"", strings.Repeat("x", MaxBytes+1), "\xff"} {
		if _, e = Parse(c, "text"); e == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if _, e = Parse("Subject: a\r\nContent-Type: multipart/mixed\r\n\r\na", "eml"); e == nil {
		t.Fatal("accepted invalid mime")
	}
}
