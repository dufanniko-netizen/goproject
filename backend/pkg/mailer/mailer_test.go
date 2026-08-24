package mailer

import (
	"strings"
	"testing"
)

func TestBuildProjectApprovalHTML(t *testing.T) {
	body := buildProjectApprovalHTML(ProjectApprovalNotification{
		RecipientName: "王主任",
		ProjectName:   "自动化改造<一期>",
		SubmitterName: "张三",
		TaskCount:     12,
		ProjectURL:    "http://example.com/project/8",
		StartDate:     "2026-08-24",
		EndDate:       "2026-09-30",
	})

	for _, expected := range []string{
		"王主任，您好",
		"自动化改造&lt;一期&gt;",
		"张三",
		"12",
		"2026-08-24",
		"2026-09-30",
		"http://example.com/project/8",
		"请勿直接回复",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("邮件正文缺少 %q", expected)
		}
	}
}

func TestEmptyAsDash(t *testing.T) {
	if got := emptyAsDash(""); got != "-" {
		t.Fatalf("空日期应显示为 -，实际为 %q", got)
	}
}
